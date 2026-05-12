#!/usr/bin/env bash
set -euo pipefail

# Bench2 Autonomous Improvement Loop
# Entry point for the 3-loop × 3-iteration improvement system.
#
# Each loop serves a different purpose:
#   Loop 1: Verifiability — replace keyword checks with verifiable ones
#   Loop 2: Semantic depth — add checks that reward structural understanding
#   Loop 3: Weight optimization — tune scoring weights for accuracy
#
# Within each loop, iterate 3 times: run → analyze → improve
#
# Usage:
#   improve-loop.sh [--loop N] [--iterations N] [--model MODEL] [--repo REPOS] [--runs N]
#
# Examples:
#   improve-loop.sh                           # Full run: 3 loops × 3 iterations
#   improve-loop.sh --loop 1 --iterations 3   # Only loop 1, 3 iterations
#   improve-loop.sh --loop 2 --repo gin,flask # Loop 2, specific repos

LOOP_DIR="$(cd "$(dirname "$0")" && pwd)"
BENCH2_DIR="$(cd "$LOOP_DIR/.." && pwd)"

LOOP_START=1
LOOP_END=3
ITERATIONS=3
MODEL=""
REPO_FILTER=""
TOOL_FILTER="sense,baseline"
RUNS=1
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --loop) LOOP_START="$2"; LOOP_END="$2"; shift 2 ;;
    --loops) LOOP_END="$2"; shift 2 ;;
    --iterations|--iterations-per-loop) ITERATIONS="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    --repo) REPO_FILTER="$2"; shift 2 ;;
    --tool) TOOL_FILTER="$2"; shift 2 ;;
    --runs) RUNS="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    -h|--help)
      echo "Usage: improve-loop.sh [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --loop N        Run only loop N (1, 2, or 3)"
      echo "  --loops N       Run loops 1 through N (default: 3)"
      echo "  --iterations N  Iterations per loop (default: 3)"
      echo "  --model MODEL   Claude model for scenario runs (e.g. sonnet)"
      echo "  --repo REPOS    Comma-separated repo filter"
      echo "  --tool TOOLS    Comma-separated tool filter (default: sense,baseline)"
      echo "  --runs N        Runs per scenario for variance (default: 1)"
      echo "  --dry-run       Show what would run without executing"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

TOTAL=$((( LOOP_END - LOOP_START + 1 ) * ITERATIONS))
echo "Starting improvement loop: loops ${LOOP_START}-${LOOP_END} × ${ITERATIONS} iterations = ${TOTAL} total"
echo ""

PHASE_ARGS=()
[[ -n "$MODEL" ]] && PHASE_ARGS+=(--model "$MODEL")
[[ -n "$REPO_FILTER" ]] && PHASE_ARGS+=(--repo "$REPO_FILTER")
[[ -n "$TOOL_FILTER" ]] && PHASE_ARGS+=(--tool "$TOOL_FILTER")
$DRY_RUN && PHASE_ARGS+=(--dry-run)

for loop in $(seq "$LOOP_START" "$LOOP_END"); do
  echo "========================================"
  loop_name="Iteration"
  case $loop in
    1) loop_name="Verifiability" ;;
    2) loop_name="Semantic Depth" ;;
    3) loop_name="Weight Optimization" ;;
  esac
  echo "  Loop $loop: $loop_name"
  echo "========================================"
  echo ""

  for iter in $(seq 1 "$ITERATIONS"); do
    echo "--- Loop $loop, Iteration $iter/$ITERATIONS ---"

    # Phase 1: Run and analyze
    bash "$LOOP_DIR/scripts/phases/phase1-run-analysis.sh" \
      --loop "$loop" \
      --runs "$RUNS" \
      "${PHASE_ARGS[@]}"

    echo ""
    echo "Phase 1 done. Waiting for analysis..."
    echo "  Read: $LOOP_DIR/results/loop-${loop}/analysis.json"
    echo "  Instructions: $LOOP_DIR/instructions/phase$([ $loop -le 3 ] && echo $loop || echo 1)-analysis-instruct.md"
    echo ""
    echo "After analysis, write improvements.json to:"
    echo "  $LOOP_DIR/results/loop-${loop}-iter-${iter}/improvements.json"
    echo ""
    echo "Then run Phase 2:"
    echo "  bash $LOOP_DIR/scripts/phases/phase2-run-improve.sh --loop $loop --iter $iter"
    echo ""
    echo "Then run Phase 3:"
    echo "  bash $LOOP_DIR/scripts/phases/phase3-run-validate.sh --loop $loop --iter $iter --runs $RUNS ${PHASE_ARGS[*]}"
    echo ""

    # In autonomous mode, phases 2 and 3 would continue here.
    # For now, pause for human/LLM analysis between phases.
    echo "Pausing for analysis. Re-run with next iteration when ready."
    exit 0
  done

  echo "Loop $loop complete."
  echo ""
done

echo "All loops complete!"
