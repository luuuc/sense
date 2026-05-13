#!/usr/bin/env bash
set -euo pipefail

# Phase 1: Run scenarios → Score → Analyze transcripts
# Prepares data for human/LLM analysis of quality markers

LOOP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
BENCH2_DIR="$(cd "$LOOP_DIR/.." && pwd)"
TOOLS_DIR="$LOOP_DIR/scripts/tools"

LOOP=1
RUNS=1
MODEL=""
REPO_FILTER=""
TOOL_FILTER="sense,baseline"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --loop) LOOP="$2"; shift 2 ;;
    --runs) RUNS="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    --repo) REPO_FILTER="$2"; shift 2 ;;
    --tool) TOOL_FILTER="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

ITER_DIR="$LOOP_DIR/results/loop-${LOOP}"
mkdir -p "$ITER_DIR"

echo "=== Phase 1: Run & Analyze (Loop $LOOP) ==="

# Step 1: Run scenarios
echo "  Running scenarios..."
run_args=(--runs "$RUNS" --tool "$TOOL_FILTER")
[[ -n "$REPO_FILTER" ]] && run_args+=(--repo "$REPO_FILTER")
[[ -n "$MODEL" ]] && run_args+=(--model "$MODEL")
$DRY_RUN && run_args+=(--dry-run)

bash "$BENCH2_DIR/run.sh" "${run_args[@]}"

if $DRY_RUN; then
  echo ""
  echo "[dry-run] Would score and analyze after run completes."
  exit 0
fi

# Step 2: Score
echo "  Scoring..."
score_args=()
[[ -n "$REPO_FILTER" ]] && score_args+=(--repo "$REPO_FILTER")
[[ -n "$TOOL_FILTER" ]] && score_args+=(--tool "$TOOL_FILTER")

bash "$BENCH2_DIR/score.sh" "${score_args[@]}"

# Step 2b: Regenerate report.md so anyone observing the loop sees fresh
# numbers immediately. Without this, report.md only updates at the end of
# improve-loop.sh — and a mid-loop crash leaves it stale for hours.
echo "  Refreshing report.md..."
bash "$BENCH2_DIR/report.sh" --md > /dev/null

# Step 3: Analyze transcripts
echo "  Analyzing transcripts..."
python3 "$TOOLS_DIR/analyze-transcripts.py" \
  --results-dir "$BENCH2_DIR/results" \
  --scenarios-dir "$BENCH2_DIR/scenarios" \
  --output "$ITER_DIR/analysis.json"

echo ""
echo "Phase 1 complete. Analysis at: $ITER_DIR/analysis.json"
echo ""
echo "Next: Read the analysis and transcripts to identify quality patterns."
echo "Instructions: $LOOP_DIR/instructions/phase1-analysis-instruct.md"
