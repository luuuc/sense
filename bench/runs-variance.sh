#!/usr/bin/env bash
# runs-variance.sh — harden a repo's numbers by running each model N times and
# reporting the spread (is the headline stable or noise?). Aggregates each
# model's N runs before the next model overwrites bench/results/, so it works
# with the single-run snapshot helper untouched.
#
#   bash bench/runs-variance.sh ruby_llm
#   MODELS="claude-opus-4-7" RUNS=5 bash bench/runs-variance.sh discourse
#
# Uses --no-build: trusts the currently installed sense binary + the existing
# .sense index (re-index the repo with the target sense version FIRST).
# Judge stays claude-opus-4-7.

set -uo pipefail
cd "$(dirname "$0")/.."

REPO="${1:?usage: runs-variance.sh <repo>}"
MODELS="${MODELS:-claude-opus-4-6 claude-opus-4-7 claude-opus-4-8 claude-fable-5}"
RUNS="${RUNS:-3}"
OUT=".doc/launch/02-rails-vertical/results/$REPO/variance.md"
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
  rm -rf "bench/results/baseline/$REPO" "bench/results/sense/$REPO"
  if bash bench/bench-sense-local.sh --tool baseline,sense --repo "$REPO" --no-build --model "$m" --runs "$RUNS"; then
    python3 bench/lib/variance-row.py "$REPO" "$m" >> "$OUT" || echo "[FAIL agg] $m"
    echo "[ok  ] $REPO / $m  done  $(date +%H:%M:%S)"
  else
    echo "[FAIL run] $REPO / $m"
    printf '\n## %s\n(run failed, rerun)\n' "$m" >> "$OUT"
  fi
done
echo "[done] $OUT"
cat "$OUT"
