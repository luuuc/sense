#!/usr/bin/env bash
set -euo pipefail

# Phase 3: Re-run scenarios → Score → Check for regressions → Apply or rollback

LOOP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
BENCH_DIR="$(cd "$LOOP_DIR/.." && pwd)"
TOOLS_DIR="$LOOP_DIR/scripts/tools"

LOOP=1
ITER=1
RUNS=1
MODEL=""
REPO_FILTER=""
TOOL_FILTER="sense,baseline"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --loop) LOOP="$2"; shift 2 ;;
    --iter) ITER="$2"; shift 2 ;;
    --runs) RUNS="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    --repo) REPO_FILTER="$2"; shift 2 ;;
    --tool) TOOL_FILTER="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

ITER_DIR="$LOOP_DIR/results/loop-${LOOP}-iter-${ITER}"
OLD_ANALYSIS="$ITER_DIR/analysis.json"

if [[ ! -f "$OLD_ANALYSIS" ]]; then
  # Try the loop-level analysis
  OLD_ANALYSIS="$LOOP_DIR/results/loop-${LOOP}/analysis.json"
fi

if [[ ! -f "$OLD_ANALYSIS" ]]; then
  echo "ERROR: No analysis.json found for comparison. Run Phase 1 first." >&2
  exit 1
fi

echo "=== Phase 3: Validate (Loop $LOOP, Iter $ITER) ==="

# Step 1: Re-run scenarios
echo "  Re-running scenarios with improved checks..."
run_args=(--runs "$RUNS" --tool "$TOOL_FILTER")
[[ -n "$REPO_FILTER" ]] && run_args+=(--repo "$REPO_FILTER")
[[ -n "$MODEL" ]] && run_args+=(--model "$MODEL")

bash "$BENCH_DIR/run.sh" "${run_args[@]}"

# Step 2: Re-score
echo "  Scoring..."
score_args=()
[[ -n "$REPO_FILTER" ]] && score_args+=(--repo "$REPO_FILTER")
[[ -n "$TOOL_FILTER" ]] && score_args+=(--tool "$TOOL_FILTER")

bash "$BENCH_DIR/score.sh" "${score_args[@]}"

# Re-judge against the new transcripts so post-analysis can compute
# fairness. Without this, post-analysis.json has sense=None /
# baseline=None for every repo and downstream convergence checks
# silently defer.
if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "  Judging..."
  judge_args=()
  [[ -n "$REPO_FILTER" ]] && judge_args+=(--repo "$REPO_FILTER")
  [[ -n "$TOOL_FILTER" ]] && judge_args+=(--tool "$TOOL_FILTER")
  # --force because the previous judged.json was scored against the
  # pre-improvement scenario YAMLs; the new transcripts need fresh
  # judgments against the post-improvement structure.
  bash "$BENCH_DIR/judge.sh" --force ${judge_args[@]+"${judge_args[@]}"} \
    > "$ITER_DIR/phase3-judge.log" 2>&1 \
    || echo "  WARN: judge.sh failed; post-analysis fairness may be incomplete"
else
  echo "  WARN: ANTHROPIC_API_KEY unset; skipping judge (post-analysis fairness will be None)"
fi

# Refresh report.md immediately so observers see fresh numbers even if the
# rest of phase 3 fails or rolls back.
bash "$BENCH_DIR/report.sh" --md > /dev/null

# Step 3: Post-run analysis (uses fresh judged.json for fairness)
echo "  Analyzing new results..."
python3 "$TOOLS_DIR/analyze-transcripts.py" \
  --results-dir "$BENCH_DIR/results" \
  --scenarios-dir "$BENCH_DIR/scenarios" \
  --output "$ITER_DIR/post-analysis.json"

# Step 4: Regression check (only compare repos that were actually changed)
MANIFEST="$ITER_DIR/changes-manifest.json"
CHANGED_REPOS=""
if [[ -f "$MANIFEST" ]]; then
  CHANGED_REPOS=$(python3 -c "
import json, sys
m = json.load(open('$MANIFEST'))
repos = [k.replace('.yaml','') for k in m]
print(','.join(repos))
")
fi

echo "  Checking for regressions (changed repos: ${CHANGED_REPOS:-all})..."
validate_args=(
  --original-dir "$ITER_DIR/backups"
  --improved-dir "$BENCH_DIR/scenarios"
  --old-scores "$OLD_ANALYSIS"
  --new-scores "$BENCH_DIR/results"
  --output "$ITER_DIR/regression.json"
)
[[ -n "$CHANGED_REPOS" ]] && validate_args+=(--changed-repos "$CHANGED_REPOS")
validate_args+=(--runs "$RUNS")

python3 "$TOOLS_DIR/validate-changes.py" "${validate_args[@]}"

REGRESSED=$(python3 -c "import json; print(json.load(open('$ITER_DIR/regression.json'))['regressed'])")
if [[ "$REGRESSED" == "True" ]]; then
  echo ""
  echo "  REGRESSION DETECTED. Rolling back..."
  cp "$ITER_DIR/backups"/*.yaml "$BENCH_DIR/scenarios/"
  bash "$BENCH_DIR/score.sh" ${score_args[@]+"${score_args[@]}"}
  bash "$BENCH_DIR/report.sh" --md > /dev/null
  echo ""
  echo "  Scenarios rolled back. See $ITER_DIR/regression.json for details."
  exit 2
fi

echo ""
echo "Phase 3 complete. No regressions."
echo "  Post-analysis: $ITER_DIR/post-analysis.json"
echo "  Regression:    $ITER_DIR/regression.json"
echo ""
echo "Next: Review results and proceed to next iteration or loop."
echo "Instructions: $LOOP_DIR/instructions/phase3-validate-instruct.md"
