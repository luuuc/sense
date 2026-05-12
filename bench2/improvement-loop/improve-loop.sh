#!/usr/bin/env bash
set -euo pipefail

# Bench2 Autonomous Improvement Loop
#
# Single loop with N iterations to convergence.
# Each iteration: run → score → analyze → Claude reviews → apply → validate
#
# Usage:
#   improve-loop.sh [--iterations N] [--reviewer-model MODEL] [--model MODEL] [--repo REPOS]
#
# Examples:
#   improve-loop.sh                                      # 3 iterations, Opus 4.7 reviewer
#   improve-loop.sh --iterations 5 --repo gin,flask      # 5 iterations, specific repos
#   improve-loop.sh --reviewer-model claude-sonnet-4-6    # Use Sonnet as reviewer

LOOP_DIR="$(cd "$(dirname "$0")" && pwd)"
BENCH2_DIR="$(cd "$LOOP_DIR/.." && pwd)"
INSTRUCT_DIR="$LOOP_DIR/instructions"

ITERATIONS=3
REVIEWER_MODEL="claude-opus-4-7"
MODEL=""
REPO_FILTER=""
TOOL_FILTER="sense,baseline"
RUNS=1
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --iterations) ITERATIONS="$2"; shift 2 ;;
    --reviewer-model) REVIEWER_MODEL="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    --repo) REPO_FILTER="$2"; shift 2 ;;
    --tool) TOOL_FILTER="$2"; shift 2 ;;
    --runs) RUNS="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    -h|--help)
      echo "Usage: improve-loop.sh [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --iterations N        Iterations to convergence (default: 3)"
      echo "  --reviewer-model M    Claude model for transcript review (default: claude-opus-4-7)"
      echo "  --model MODEL         Claude model for scenario runs"
      echo "  --repo REPOS          Comma-separated repo filter"
      echo "  --tool TOOLS          Comma-separated tool filter (default: sense,baseline)"
      echo "  --runs N              Runs per scenario for variance (default: 1)"
      echo "  --dry-run             Show what would run without executing"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

echo "Starting improvement loop: $ITERATIONS iterations, reviewer=$REVIEWER_MODEL"
echo ""

PHASE_ARGS=()
[[ -n "$MODEL" ]] && PHASE_ARGS+=(--model "$MODEL")
[[ -n "$REPO_FILTER" ]] && PHASE_ARGS+=(--repo "$REPO_FILTER")
[[ -n "$TOOL_FILTER" ]] && PHASE_ARGS+=(--tool "$TOOL_FILTER")
$DRY_RUN && PHASE_ARGS+=(--dry-run)

for iter in $(seq 1 "$ITERATIONS"); do
  echo "========================================"
  echo "  Iteration $iter/$ITERATIONS"
  echo "========================================"
  echo ""

  ITER_DIR="$LOOP_DIR/results/loop-1-iter-${iter}"
  mkdir -p "$ITER_DIR"

  # ── Phase 1: Run scenarios, score, analyze ──
  echo "--- Phase 1: Run & Analyze ---"
  bash "$LOOP_DIR/scripts/phases/phase1-run-analysis.sh" \
    --loop 1 \
    --runs "$RUNS" \
    "${PHASE_ARGS[@]}"

  if $DRY_RUN; then
    echo "[dry-run] Would invoke Claude reviewer and continue."
    continue
  fi

  # ── Claude reviewer: read transcripts, generate improvements.json ──
  echo ""
  echo "--- Transcript Review (model: $REVIEWER_MODEL) ---"

  REVIEW_PROMPT="$(cat <<PROMPT
Read the following instruction files, then analyze the bench2 transcripts and generate improvements.json.

## Instructions

$(cat "$INSTRUCT_DIR/LOOP-CONTEXT.md")

---

$(cat "$INSTRUCT_DIR/phase1-analysis-instruct.md")

---

$(cat "$INSTRUCT_DIR/phase2-improve-instruct.md")

## Current Scores

$(python3 "$BENCH2_DIR/lib/reporter.py" "$BENCH2_DIR/results" --format terminal 2>/dev/null)

## Task

1. Read the sense and baseline transcripts for all 6 repos in $BENCH2_DIR/results/
2. Write analysis-notes.md to $ITER_DIR/analysis-notes.md
3. Write improvements.json to $ITER_DIR/improvements.json

Output ONLY the improvements.json content to stdout when done.
PROMPT
)"

  claude -p "$REVIEW_PROMPT" \
    --model "$REVIEWER_MODEL" \
    --allowedTools "Read,Bash,Write" \
    > "$ITER_DIR/claude-review.log" 2>&1

  if [[ ! -f "$ITER_DIR/improvements.json" ]]; then
    echo "ERROR: Reviewer did not produce improvements.json" >&2
    echo "Check $ITER_DIR/ for partial output." >&2
    exit 1
  fi

  echo "  Improvements generated: $ITER_DIR/improvements.json"

  # ── Phase 2: Apply improvements ──
  echo ""
  echo "--- Phase 2: Apply Improvements ---"
  bash "$LOOP_DIR/scripts/phases/phase2-run-improve.sh" \
    --loop 1 \
    --iter "$iter"

  # ── Phase 3: Re-run, score, validate ──
  echo ""
  echo "--- Phase 3: Validate ---"
  PHASE3_EXIT=0
  bash "$LOOP_DIR/scripts/phases/phase3-run-validate.sh" \
    --loop 1 \
    --iter "$iter" \
    --runs "$RUNS" \
    "${PHASE_ARGS[@]}" || PHASE3_EXIT=$?

  if [[ $PHASE3_EXIT -eq 2 ]]; then
    echo ""
    echo "  Regression detected in iteration $iter. Scenarios rolled back."
    echo "  Stopping loop — review $ITER_DIR/regression.json before retrying."
    exit 2
  elif [[ $PHASE3_EXIT -ne 0 ]]; then
    echo "ERROR: Phase 3 failed with exit code $PHASE3_EXIT" >&2
    exit $PHASE3_EXIT
  fi

  echo ""
  echo "Iteration $iter complete."
  echo ""

  # ── Convergence check ──
  if [[ $iter -gt 1 ]]; then
    PREV_DIR="$LOOP_DIR/results/loop-1-iter-$((iter - 1))"
    if [[ -f "$PREV_DIR/post-analysis.json" && -f "$ITER_DIR/post-analysis.json" ]]; then
      CONVERGED=$(python3 -c "
import json, sys
try:
    old = json.load(open('$PREV_DIR/post-analysis.json'))
    new = json.load(open('$ITER_DIR/post-analysis.json'))
    old_gap = abs(old.get('sense_avg', 0) - old.get('baseline_avg', 0))
    new_gap = abs(new.get('sense_avg', 0) - new.get('baseline_avg', 0))
    print('true' if abs(old_gap - new_gap) < 0.02 else 'false')
except Exception:
    print('false')
")
      if [[ "$CONVERGED" == "true" ]]; then
        echo "Converged — fairness gap stable within 0.02. Stopping."
        break
      fi
    fi
  fi
done

echo ""
echo "========================================"
echo "  Loop complete after $iter iterations"
echo "========================================"

# Final report
echo ""
python3 "$BENCH2_DIR/lib/reporter.py" "$BENCH2_DIR/results" --format terminal 2>/dev/null
echo ""
echo "Run 'bash bench2/report.sh --md' to regenerate report.md."
