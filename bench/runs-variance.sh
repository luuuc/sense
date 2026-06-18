#!/usr/bin/env bash
# runs-variance.sh — harden a repo's numbers by running each model N times and
# reporting the spread (is the headline stable or noise?). Aggregates each
# model's N runs before the next model overwrites bench/results/, so it works
# with the single-run snapshot helper untouched.
#
#   bash bench/runs-variance.sh ruby_llm
#   MODELS="claude-opus-4-8" RUNS=5 bash bench/runs-variance.sh discourse
#
# Uses --no-build: trusts the currently installed sense binary + the existing
# .sense index (re-index the repo with the target sense version FIRST).
# Judge stays claude-opus-4-7.

set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$BENCH_DIR/.."
# Vertical wrapper: defaults to the ruby-rails vertical (baseline vs sense only),
# overridable with VERTICAL= (empty = global). Exports RESULTS_DIR/SCENARIOS_DIR so
# bench-sense-local.sh and variance-row.py write/read the vertical subtree.
VERTICAL="${VERTICAL-ruby-rails}"
source "$BENCH_DIR/lib/bench-paths.sh"

REPO="${1:?usage: runs-variance.sh <repo>}"
MODELS="${MODELS:-claude-opus-4-6 claude-opus-4-8}"
RUNS="${RUNS:-3}"
# Per-repo run-spread report, across models. Lives in the tracked bench tree (the
# vertical base, not a model root) alongside the per-model reports + matrix.
OUT="$RESULTS_DIR/variance/$REPO.md"
mkdir -p "$(dirname "$OUT")"

RUN_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
REPO_CLONE="${SENSE_CLONES:-/Users/luc/Developer/luuuc/oss/sense-benchmark/sense}/$REPO"
REPO_SHA="$(git -C "$REPO_CLONE" rev-parse --short HEAD 2>/dev/null || echo '?')"
echo "# $REPO — variance ($RUNS runs per model)" > "$OUT"
echo "" >> "$OUT"
echo "**run date (UTC):** $RUN_DATE  ·  **models:** $MODELS  ·  **repo:** $REPO @ $REPO_SHA" >> "$OUT"
echo "sense: $(sense --version 2>/dev/null | head -1)  ·  judge: claude-opus-4-7" >> "$OUT"

echo "[variance] $REPO : $RUNS runs x {$MODELS}"
for m in $MODELS; do
  echo "[run ] $REPO / $m  x$RUNS  start $(date +%H:%M:%S)"
  # Re-resolve RESULTS_DIR to this model's own root so models never overwrite.
  unset RESULTS_DIR; export BENCH_MODEL="$m"; source "$BENCH_DIR/lib/bench-paths.sh"
  rm -rf "$RESULTS_DIR/baseline/$REPO" "$RESULTS_DIR/sense/$REPO"
  ok=1
  # Dispatch by model id (mirrors sweep-rails). bench-sense-local takes --runs and
  # writes run-N itself; the cloud/codex runners are single-run, so we invoke them
  # RUNS times and file each invocation's flat output into a run-N/ subdir, giving
  # variance-row.py / pergroup.py the same run-N shape on every harness.
  case "$m" in
    *:cloud|ollama-cloud/*|ollama/*) runner=(bash bench/opencode-run.sh --tool baseline,sense --repo "$REPO" --model "$m"); per_run=1 ;;
    codex:*)                         runner=(bash bench/codex-run.sh    --tool baseline,sense --repo "$REPO" --model "${m#codex:}"); per_run=1 ;;
    gpt-*|o3*|o4*)                   runner=(bash bench/codex-run.sh    --tool baseline,sense --repo "$REPO" --model "$m"); per_run=1 ;;
    *)                               runner=(bash bench/bench-sense-local.sh --tool baseline,sense --repo "$REPO" --no-build --model "$m" --runs "$RUNS"); per_run=0 ;;
  esac
  if [ "$per_run" = 0 ]; then
    "${runner[@]}" || ok=0
  else
    for k in $(seq 1 "$RUNS"); do
      echo "[run ] $REPO / $m  run $k/$RUNS"
      if ! "${runner[@]}"; then ok=0; break; fi
      for arm in baseline sense; do
        rd="$RESULTS_DIR/$arm/$REPO"
        [ -d "$rd" ] || continue
        mkdir -p "$rd/run-$k"
        find "$rd" -maxdepth 1 -type f -exec mv {} "$rd/run-$k/" \;
      done
    done
  fi
  if [ "$ok" = 1 ]; then
    python3 bench/lib/variance-row.py "$REPO" "$m" >> "$OUT" || echo "[FAIL agg] $m"
    echo "[ok  ] $REPO / $m  done  $(date +%H:%M:%S)"
  else
    echo "[FAIL run] $REPO / $m"
    printf '\n## %s\n(run failed, rerun)\n' "$m" >> "$OUT"
  fi
done
echo "[done] $OUT"
cat "$OUT"
# Refresh the vertical's cross-model matrix (results/vertical/<name>/report.md|json).
bash "$BENCH_DIR/report-matrix.sh" >/dev/null 2>&1 || echo "[warn] matrix refresh failed" >&2
