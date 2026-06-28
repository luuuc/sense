#!/usr/bin/env bash
# runs-variance.sh — harden a repo's numbers by running each model N times and
# reporting the spread (is the headline stable or noise?). Aggregates each
# model's N runs before the next model overwrites bench/results/, so it works
# with the single-run snapshot helper untouched.
#
#   bash bench/drivers/runs-variance.sh ruby_llm
#   MODELS="claude-opus-4-8" RUNS=5 bash bench/drivers/runs-variance.sh discourse
#
# Uses --no-build: trusts the currently installed sense binary + the existing
# .sense index (re-index the repo with the target sense version FIRST).
# Judge stays claude-sonnet-4-6.

set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$BENCH_DIR/.."
# Vertical wrapper: defaults to the ruby-rails vertical (baseline vs sense only),
# overridable with VERTICAL= (empty = global). Exports RESULTS_DIR/SCENARIOS_DIR so
# bench-sense-local.sh and variance-row.py write/read the vertical subtree.
VERTICAL="${VERTICAL-ruby-rails}"
source "$BENCH_DIR/lib/bench-paths.sh"
# Subscription-throttle pacing helpers. Used ONLY in the metered opencode/codex
# dispatch (per_run=1 below); the claude/opus branch never calls a pace_* helper,
# so the opus path is byte-unchanged. Default-on; BENCH_THROTTLE_PACING=0 = no-op.
source "$BENCH_DIR/lib/throttle-pacing.sh"

REPO="${1:?usage: runs-variance.sh <repo>}"
MODELS="${MODELS:-claude-opus-4-8}"
RUNS="${RUNS:-2}"   # RUNS=2 is the campaign cost cap for all vertical benches (×3 costs too much)
# Breadth-first append knobs (metered per_run=1 arms). START_RUN shifts run
# numbering so a later pass files run-2 without redoing run-1; KEEP_RUNS=1 skips
# the per-repo wipe so the new run is ADDED beside existing run dirs. Defaults
# (START_RUN=1, KEEP_RUNS=0) reproduce the exact prior behavior byte-for-byte.
START_RUN="${START_RUN:-1}"
KEEP_RUNS="${KEEP_RUNS:-0}"
LAST_RUN=$((START_RUN + RUNS - 1))
# Per-repo run-spread report, across models. Lives in the tracked bench tree (the
# vertical base, not a model root) alongside the per-model reports + matrix.
OUT="$RESULTS_DIR/variance/$REPO.md"
mkdir -p "$(dirname "$OUT")"

RUN_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
REPO_CLONE="${SENSE_CLONES:-$HOME/Developer/luuuc/oss/sense-benchmark/sense}/$REPO"
REPO_SHA="$(git -C "$REPO_CLONE" rev-parse --short HEAD 2>/dev/null || echo '?')"
echo "# $REPO — variance ($RUNS runs per model)" > "$OUT"
echo "" >> "$OUT"
echo "**run date (UTC):** $RUN_DATE  ·  **models:** $MODELS  ·  **repo:** $REPO @ $REPO_SHA" >> "$OUT"
echo "sense: $(sense --version 2>/dev/null | head -1)  ·  judge: ${BENCH_JUDGE_MODEL:-claude-sonnet-4-6}" >> "$OUT"

# Ensure the repo's Sense index matches the CURRENT scan engine before benching any
# model — the index is shared across all models, so this runs once per repo. Rebuilds
# only when the scan-engine fingerprint changed (a no-op release reuses the existing
# index); skips instantly when fresh. This replaces the old "blindly rebuild every repo"
# prereq. Set SKIP_ENSURE_INDEX=1 to bypass (e.g. a host without the Go toolchain).
if [ "${SKIP_ENSURE_INDEX:-0}" != 1 ]; then
  bash "$BENCH_DIR/lib/ensure-index.sh" "$REPO" \
    || echo "[warn] ensure-index could not verify $REPO; benching the existing index as-is" >&2
fi

echo "[variance] $REPO : $RUNS runs x {$MODELS}"
for m in $MODELS; do
  echo "[run ] $REPO / $m  x$RUNS  start $(date +%H:%M:%S)"
  # Re-resolve RESULTS_DIR to this model's own root so models never overwrite.
  unset RESULTS_DIR; export BENCH_MODEL="$m"; source "$BENCH_DIR/lib/bench-paths.sh"
  [ "$KEEP_RUNS" = 1 ] || rm -rf "$RESULTS_DIR/baseline/$REPO" "$RESULTS_DIR/sense/$REPO"
  ok=1
  # Dispatch by model id (mirrors sweep.sh). bench-sense-local takes --runs and
  # writes run-N itself; the cloud/codex runners are single-run, so we invoke them
  # RUNS times and file each invocation's flat output into a run-N/ subdir, giving
  # variance-row.py / pergroup.py the same run-N shape on every harness.
  case "$m" in
    kimi-for-coding/*|zai-coding-plan/*|zhipuai-coding-plan/*|minimax-coding-plan/*|minimax-cn-coding-plan/*|alibaba-coding-plan/*|alibaba-coding-plan-cn/*|moonshotai/*|moonshotai-cn/*|*:cloud|ollama-cloud/*|ollama/*) \
                                     runner=(bash bench/drivers/opencode-run.sh --tool baseline,sense --repo "$REPO" --model "$m"); per_run=1 ;;
    codex:*)                         runner=(bash bench/drivers/codex-run.sh    --tool baseline,sense --repo "$REPO" --model "${m#codex:}"); per_run=1 ;;
    gpt-*|o3*|o4*)                   runner=(bash bench/drivers/codex-run.sh    --tool baseline,sense --repo "$REPO" --model "$m"); per_run=1 ;;
    *)                               runner=(bash bench/drivers/bench-sense-local.sh --tool baseline,sense --repo "$REPO" --no-build --model "$m" --runs "$RUNS"); per_run=0 ;;
  esac
  if [ "$per_run" = 0 ]; then
    "${runner[@]}" || ok=0
  else
    for k in $(seq "$START_RUN" "$LAST_RUN"); do
      echo "[run ] $REPO / $m  run $k/$LAST_RUN"
      if ! "${runner[@]}"; then ok=0; break; fi
      for arm in baseline sense; do
        rd="$RESULTS_DIR/$arm/$REPO"
        [ -d "$rd" ] || continue
        mkdir -p "$rd/run-$k"
        find "$rd" -maxdepth 1 -type f -exec mv {} "$rd/run-$k/" \;
      done
      # Throttle-onset cooldown + inter-run spacing for the metered arms only.
      # Read this run's run_meta (both arms) to classify the session; after N
      # consecutive degraded sessions pace_note_session pauses a window-reset
      # cooldown. Then space the next run. The opus path never reaches here.
      cls="$(pace_session_classify "$REPO" "$RESULTS_DIR" "$k")"
      pace_note_session "$cls"
      [ "$k" -lt "$LAST_RUN" ] && pace_sleep "$OPENCODE_PACE_SECONDS" "between runs ($REPO run $k of $LAST_RUN)"
    done
  fi
  if [ "$ok" = 1 ]; then
    python3 bench/lib/variance-row.py "$REPO" "$m" >> "$OUT" || echo "[FAIL agg] $m"
    # Refresh this model's per-repo report + citation check from the run-* data
    # just written (RESULTS_DIR is this model's root; VERTICAL is set, so report.sh
    # renders the plain-English vertical prose). Keeps <model>/report.md from
    # going stale after a variance run.
    bash "$BENCH_DIR/report.sh" --md  >/dev/null 2>&1 || echo "[warn] $m per-model report.md refresh failed" >&2
    bash "$BENCH_DIR/report.sh" --json >/dev/null 2>&1 || echo "[warn] $m per-model report.json refresh failed" >&2
    echo "[ok  ] $REPO / $m  done  $(date +%H:%M:%S)"
  else
    echo "[FAIL run] $REPO / $m"
    printf '\n## %s\n(run failed, rerun)\n' "$m" >> "$OUT"
  fi
done
echo "[done] $OUT"
cat "$OUT"
# Refresh the vertical's cross-model matrix (verticals/<name>/results/report.md|json).
bash "$BENCH_DIR/drivers/report-matrix.sh" >/dev/null 2>&1 || echo "[warn] matrix refresh failed" >&2
