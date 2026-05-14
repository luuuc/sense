#!/usr/bin/env bash
set -euo pipefail

# Phase 4: Audit — runs three auditor roles after Phase 3 validates.
#
#   1. Score auditor    — one judge call per (tool, repo) batched across steps,
#                         emits audit-scoring.{tool}.{repo}.json + ...-full.json
#   2. Scenario auditor — one judge call per repo (both transcripts side-by-side),
#                         emits audit-scenarios.{repo}.json
#   3. Watchdog         — single judge call comparing before/after snapshots,
#                         emits audit-watchdog.json
#
# After auditing, capture a post-iteration snapshot so the next iteration's
# watchdog has a "before" to compare against.
#
# Auditors run in parallel; each appends to its own log under the iter dir.

LOOP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
BENCH_DIR="$(cd "$LOOP_DIR/.." && pwd)"
LIB_DIR="$BENCH_DIR/lib"

LOOP=1
ITER=1
TOOL_FILTER="sense,baseline"
REPO_FILTER=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --loop) LOOP="$2"; shift 2 ;;
    --iter) ITER="$2"; shift 2 ;;
    --tool) TOOL_FILTER="$2"; shift 2 ;;
    --repo) REPO_FILTER="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "phase4: ANTHROPIC_API_KEY not set — skipping audit phase" >&2
  exit 0
fi

ITER_DIR="$LOOP_DIR/results/loop-${LOOP}-iter-${ITER}"
LOOP_DIR_RESULTS="$LOOP_DIR/results/loop-${LOOP}"
mkdir -p "$ITER_DIR" "$LOOP_DIR_RESULTS"

echo "=== Phase 4: Audit (Loop $LOOP, Iter $ITER) ==="

# Ensure judged.json is up-to-date for the current results — both
# auditors and the watchdog read it. judge.sh is idempotent (skips when
# judged.json is newer than transcript.json).
echo "  Refreshing judged.json (idempotent)..."
judge_args=()
[[ -n "$TOOL_FILTER" ]] && judge_args+=(--tool "$TOOL_FILTER")
[[ -n "$REPO_FILTER" ]] && judge_args+=(--repo "$REPO_FILTER")
bash "$BENCH_DIR/judge.sh" ${judge_args[@]+"${judge_args[@]}"} \
  > "$ITER_DIR/phase4-judge.log" 2>&1 || {
  echo "  WARN: judge.sh failed; see $ITER_DIR/phase4-judge.log" >&2
}

# Resolve filters
IFS=',' read -ra TOOLS <<< "$TOOL_FILTER"
if [[ -n "$REPO_FILTER" ]]; then
  IFS=',' read -ra REPOS <<< "$REPO_FILTER"
else
  REPOS=()
  for d in "$BENCH_DIR/results/${TOOLS[0]}"/*/; do
    [[ -d "$d" ]] && REPOS+=("$(basename "$d")")
  done
fi

# ── Score auditor: parallel per (tool, repo) ───────────────────────────
echo "  Running score auditor (parallel: ${#TOOLS[@]} tools × ${#REPOS[@]} repos)..."
PIDS=()
for tool in "${TOOLS[@]}"; do
  for repo in "${REPOS[@]}"; do
    scored="$BENCH_DIR/results/$tool/$repo/scored.json"
    transcript="$BENCH_DIR/results/$tool/$repo/transcript.json"
    scenario="$BENCH_DIR/scenarios/${repo}.yaml"
    if [[ ! -f "$scored" || ! -f "$transcript" || ! -f "$scenario" ]]; then
      echo "    skip $tool/$repo (missing scored/transcript/scenario)" >&2
      continue
    fi
    out="$ITER_DIR/audit-scoring.${tool}.${repo}.json"
    log="$ITER_DIR/audit-scoring.${tool}.${repo}.log"
    (
      python3 "$LIB_DIR/audit_scoring.py" "$scored" "$transcript" "$scenario" \
        --out "$out" > "$log" 2>&1
    ) &
    PIDS+=($!)
  done
done
wait "${PIDS[@]}" || true

# ── Scenario auditor: parallel per repo ────────────────────────────────
echo "  Running scenario auditor (parallel: ${#REPOS[@]} repos)..."
PIDS=()
for repo in "${REPOS[@]}"; do
  scenario="$BENCH_DIR/scenarios/${repo}.yaml"
  if [[ ! -f "$scenario" ]]; then
    continue
  fi
  out="$ITER_DIR/audit-scenarios.${repo}.json"
  log="$ITER_DIR/audit-scenarios.${repo}.log"
  (
    python3 "$LIB_DIR/audit_scenarios.py" "$scenario" "$BENCH_DIR/results" \
      --out "$out" > "$log" 2>&1
  ) &
  PIDS+=($!)
done
wait "${PIDS[@]}" || true

# ── Post-snapshot for this iteration ───────────────────────────────────
POST_SNAPSHOT="$ITER_DIR/post-snapshot.json"
python3 "$LIB_DIR/audit_watchdog.py" snapshot \
  --results-root "$BENCH_DIR/results" \
  --out "$POST_SNAPSHOT" 2> "$ITER_DIR/post-snapshot.log"

# ── Watchdog: compare prior snapshot to post ───────────────────────────
PREV_SNAPSHOT=""
if [[ "$ITER" =~ ^[0-9]+$ ]] && (( ITER > 1 )); then
  PREV_SNAPSHOT="$LOOP_DIR/results/loop-${LOOP}-iter-$((ITER - 1))/post-snapshot.json"
fi
if [[ -z "$PREV_SNAPSHOT" || ! -f "$PREV_SNAPSHOT" ]]; then
  PREV_SNAPSHOT="$LOOP_DIR_RESULTS/baseline-snapshot.json"
fi

if [[ -f "$PREV_SNAPSHOT" ]]; then
  echo "  Running watchdog (before=$PREV_SNAPSHOT, after=$POST_SNAPSHOT)..."
  python3 "$LIB_DIR/audit_watchdog.py" audit \
    --before-snapshot "$PREV_SNAPSHOT" \
    --after-snapshot "$POST_SNAPSHOT" \
    --improvements "$ITER_DIR/improvements.json" \
    --out "$ITER_DIR/audit-watchdog.json" \
    > "$ITER_DIR/audit-watchdog.log" 2>&1 || {
    echo "  WARN: watchdog failed; see $ITER_DIR/audit-watchdog.log" >&2
  }
else
  echo "  WARN: no before-snapshot available; skipping watchdog" >&2
fi

# ── Meta-report ────────────────────────────────────────────────────────
echo "  Rendering meta-report..."
python3 "$LIB_DIR/meta_report.py" \
  --iter-dir "$ITER_DIR" \
  --iter "$ITER" \
  --out "$ITER_DIR/meta-report.md" \
  > "$ITER_DIR/meta-report.log" 2>&1 || {
  echo "  WARN: meta-report rendering failed; see $ITER_DIR/meta-report.log" >&2
}

echo ""
echo "Phase 4 complete."
echo "  Score audits:    $ITER_DIR/audit-scoring.*.json"
echo "  Scenario audits: $ITER_DIR/audit-scenarios.*.json"
echo "  Watchdog:        $ITER_DIR/audit-watchdog.json"
echo "  Snapshot:        $ITER_DIR/post-snapshot.json"
echo "  Meta-report:     $ITER_DIR/meta-report.md"
