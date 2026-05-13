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

HISTORY_FILE="$LOOP_DIR/results/loop-1/iteration-history.jsonl"
mkdir -p "$LOOP_DIR/results/loop-1"

# Baseline snapshot — captured once before any iteration runs so iter-1's
# watchdog has a "before" reference point. Skipped on dry-run and when
# ANTHROPIC_API_KEY is missing (the auditor stack is API-gated; the loop
# still runs without it).
BASELINE_SNAPSHOT="$LOOP_DIR/results/loop-1/baseline-snapshot.json"
if ! $DRY_RUN && [[ -n "${ANTHROPIC_API_KEY:-}" && ! -f "$BASELINE_SNAPSHOT" ]]; then
  echo "Capturing baseline snapshot for watchdog..."
  python3 "$BENCH2_DIR/lib/audit_watchdog.py" snapshot \
    --results-root "$BENCH2_DIR/results" \
    --out "$BASELINE_SNAPSHOT" || true
fi

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

  HISTORY_SECTION=""
  if [[ -f "$HISTORY_FILE" && -s "$HISTORY_FILE" ]]; then
    HISTORY_SECTION="## Previous Iterations

The following iterations have already been attempted. Do NOT re-propose changes that caused regressions or were rolled back. Learn from what worked and what failed.

$(cat "$HISTORY_FILE")
"
  fi

  # ── Scenario-auditor hints from the prior iter (if any) ──
  AUDIT_HINTS_SECTION=""
  if [[ $iter -gt 1 ]]; then
    PREV_AUDIT_DIR="$LOOP_DIR/results/loop-1-iter-$((iter - 1))"
    AUDIT_FILES=("$PREV_AUDIT_DIR"/audit-scenarios.*.json)
    if [[ -f "${AUDIT_FILES[0]}" ]]; then
      AUDIT_HINTS_SECTION="## Auditor hints (from iter $((iter - 1)))

The scenario auditor proposed the following check changes for the prior
iteration's state. Treat each as a *hint*, not a command — adopt, refine,
or reject with rationale. Always cite transcript evidence in the
modification's \`evidence\` field, even when an auditor proposed it. The
Phase 3 rollback safety net still protects against bad proposals.

$(cat "${AUDIT_FILES[@]}" 2>/dev/null)
"
    fi
  fi

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

${HISTORY_SECTION}${AUDIT_HINTS_SECTION}## Task

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

  ITER_OUTCOME="applied"
  if [[ $PHASE3_EXIT -eq 2 ]]; then
    ITER_OUTCOME="rolled_back"
    echo ""
    echo "  Regression detected in iteration $iter. Scenarios rolled back."
  elif [[ $PHASE3_EXIT -ne 0 ]]; then
    echo "ERROR: Phase 3 failed with exit code $PHASE3_EXIT" >&2
    exit $PHASE3_EXIT
  fi

  # ── Phase 4: Audit (score / scenario / watchdog) ──
  echo ""
  echo "--- Phase 4: Audit ---"
  phase4_args=(--loop 1 --iter "$iter" --tool "$TOOL_FILTER")
  [[ -n "$REPO_FILTER" ]] && phase4_args+=(--repo "$REPO_FILTER")
  bash "$LOOP_DIR/scripts/phases/phase4-audit.sh" "${phase4_args[@]}" || {
    echo "  WARN: Phase 4 had errors; auditor outputs may be incomplete." >&2
  }

  # ── Record iteration history ──
  python3 -c "
import json, os
iter_dir = '$ITER_DIR'
entry = {'iteration': $iter, 'outcome': '$ITER_OUTCOME'}
imp_path = os.path.join(iter_dir, 'improvements.json')
if os.path.exists(imp_path):
    with open(imp_path) as f:
        entry['improvements'] = json.load(f)
post_path = os.path.join(iter_dir, 'post-analysis.json')
if os.path.exists(post_path):
    with open(post_path) as f:
        post = json.load(f)
    entry['scores_after'] = {r: d['current_scores'] for r, d in post.get('repos', {}).items()}
reg_path = os.path.join(iter_dir, 'regression.json')
if os.path.exists(reg_path):
    with open(reg_path) as f:
        entry['regression_check'] = json.load(f)
print(json.dumps(entry))
" >> "$HISTORY_FILE"

  echo ""
  echo "Iteration $iter complete ($ITER_OUTCOME)."
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
    def avg_gap(d):
        gaps = [r['current_scores']['gap'] for r in d.get('repos', {}).values()
                if r.get('current_scores', {}).get('sense') is not None]
        return sum(gaps) / len(gaps) if gaps else 0
    print('true' if abs(avg_gap(old) - avg_gap(new)) < 0.02 else 'false')
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

# Final score + reports
echo ""
echo "--- Scoring final results ---"
bash "$BENCH2_DIR/score.sh"
echo ""
bash "$BENCH2_DIR/report.sh" --md
echo ""
bash "$BENCH2_DIR/report.sh" --json
echo ""
bash "$BENCH2_DIR/report.sh"

# Freshness guard: report.md must be at least as new as the newest
# scored.json. A stale report after a successful loop means report.sh ran
# but failed silently — surface it loudly instead of letting the next loop
# inherit yesterday's numbers.
REPORT_MD="$BENCH2_DIR/results/report.md"
if [[ ! -f "$REPORT_MD" ]]; then
  echo "ERROR: $REPORT_MD was not generated" >&2
  exit 1
fi
NEWEST_SCORE=$(find "$BENCH2_DIR/results" -name 'scored.json' -type f -print0 \
  | xargs -0 stat -f '%m' 2>/dev/null | sort -nr | head -1)
REPORT_MTIME=$(stat -f '%m' "$REPORT_MD" 2>/dev/null)
if [[ -n "$NEWEST_SCORE" && -n "$REPORT_MTIME" && "$REPORT_MTIME" -lt "$NEWEST_SCORE" ]]; then
  echo "ERROR: report.md is older than the newest scored.json — report.sh did not refresh it." >&2
  exit 1
fi
