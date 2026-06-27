#!/usr/bin/env bash
# session-run.sh — multi-turn ("real work session") bench.
#
# Runs each scenario STEP as a separate conversational turn in the SAME claude
# session: turn 1 fresh (captures its session_id), turns 2..N via --resume, so
# context accumulates the way it does in real daily use. Concatenates every
# turn's stream-json into one transcript.json and sums wall time, then
# score -> judge (--via-cli) -> report. Subscription only (no API key);
# judge stays claude-sonnet-4-6 (set in judge.py).
#
#   bash bench/drivers/session-run.sh --tool baseline,sense --repo discourse [--model M]
#
# Prereqs: clones at $SENSE_BENCH_ROOT/{baseline,sense}/<repo>, sense arm
# already `sense setup` + `sense scan`-ed (this runner does not build/scan).

set -uo pipefail

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
# Vertical wrapper: defaults to the ruby-rails vertical (baseline vs sense only),
# overridable with VERTICAL= (empty = global). bench-paths.sh resolves the roots
# and exports them so the workers it calls (score/judge/report) inherit them.
VERTICAL="${VERTICAL-ruby-rails}"
source "$BENCH_DIR/lib/bench-paths.sh"
LIB_DIR="$BENCH_DIR/lib"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"
BASELINE_MCP="$LIB_DIR/baseline-mcp.json"

TOOLS_CSV="baseline,sense"; REPO=""; MODEL="claude-opus-4-8"; TURN_TIMEOUT=600
while [[ $# -gt 0 ]]; do case "$1" in
  --tool) TOOLS_CSV="$2"; shift 2;;
  --repo) REPO="$2"; shift 2;;
  --model) MODEL="$2"; shift 2;;
  --timeout) TURN_TIMEOUT="$2"; shift 2;;
  *) echo "unknown arg: $1" >&2; exit 1;;
esac; done
[[ -n "$REPO" ]] || { echo "need --repo" >&2; exit 1; }

# Now that --model is known, re-resolve RESULTS_DIR to this model's own root
# (vertical benches are model-scoped so models never overwrite each other).
unset RESULTS_DIR; export BENCH_MODEL="$MODEL"; source "$BENCH_DIR/lib/bench-paths.sh"

unset ANTHROPIC_API_KEY BENCHMARK_ANTHROPIC_API_KEY
command -v claude >/dev/null || { echo "claude CLI not found" >&2; exit 1; }
# Fold the per-turn ceiling into the prefix (or empty if no timeout binary, so
# the command stays `claude ...`, not `600 claude ...`). macOS has no `timeout`.
if command -v timeout >/dev/null; then TO=(timeout "$TURN_TIMEOUT")
elif command -v gtimeout >/dev/null; then TO=(gtimeout "$TURN_TIMEOUT")
else TO=(env); fi   # `env` is a no-op prefix; avoids empty-array + `set -u` on bash 3.2

SCEN="$SCENARIOS_DIR/$REPO.yaml"
[[ -f "$SCEN" ]] || { echo "no scenario $SCEN" >&2; exit 1; }
NSTEPS=$(python3 -c "import yaml;print(len(yaml.safe_load(open('$SCEN'))['steps']))")
SCEN_NAME=$(python3 -c "import yaml;print(yaml.safe_load(open('$SCEN'))['name'])")
DESC=$(python3 -c "import yaml;print((yaml.safe_load(open('$SCEN')).get('description') or '').strip())")
SVER="$(sense --version 2>/dev/null | head -1 || echo '')"

IFS=',' read -ra TOOLS <<< "$TOOLS_CSV"
for tool in "${TOOLS[@]}"; do
  repo_dir="$SENSE_BENCH_ROOT/$tool/$REPO"
  [[ -d "$repo_dir/.git" ]] || { echo "[session] SKIP $tool: clone missing at $repo_dir" >&2; continue; }
  out="$RESULTS_DIR/$tool/$REPO"; mkdir -p "$out"; : > "$out/transcript.json"; : > "$out/claude.log"
  echo "[session] $tool/$REPO model=$MODEL steps=$NSTEPS" >&2

  sid=""; total_wall=0
  for ((i=0; i<NSTEPS; i++)); do
    prompt=$(python3 -c "import yaml;print(yaml.safe_load(open('$SCEN'))['steps'][$i]['prompt'])")
    if [[ $i -eq 0 ]]; then
      turn="You are working in the $REPO repository. $DESC

Task 1 of $NSTEPS: $prompt"
      args=(-p "$turn")
    else
      turn="Task $((i+1)) of $NSTEPS (same codebase, same session): $prompt"
      args=(-p "$turn" --resume "$sid")
    fi
    args+=(--verbose --output-format stream-json --permission-mode bypassPermissions --disallowed-tools Agent --model "$MODEL")
    # Isolate MCP per arm. baseline = empty config (true no-code-intel control).
    # sense = ONLY the clone's own sense server (strict), so the operator's
    # global claude.ai servers (Gmail/Calendar/Drive) don't leak in and don't
    # compete with or delay the Sense connection — the bug that left the sense
    # arm running as grep (status: pending) and invalidated the first run.
    if [[ "$tool" == baseline ]]; then
      args+=(--strict-mcp-config --mcp-config "$BASELINE_MCP")
    else
      args+=(--strict-mcp-config --mcp-config "$repo_dir/.mcp.json")
    fi

    start=$(date +%s)
    ( cd "$repo_dir" && IS_SANDBOX=1 "${TO[@]}" claude "${args[@]}" ) >> "$out/transcript.json" 2>> "$out/claude.log"
    rc=$?
    total_wall=$(( total_wall + $(date +%s) - start ))
    if [[ $i -eq 0 ]]; then
      sid=$(grep -oE '"session_id":"[^"]+"' "$out/transcript.json" | head -1 | cut -d'"' -f4)
      [[ -n "$sid" ]] || { echo "[session] FATAL: no session_id from turn 1 ($tool)" >&2; break; }
      if [[ "$tool" == sense ]] && ! grep -q '"name":"sense","status":"connected"' "$out/transcript.json"; then
        echo "[session]   WARN: Sense MCP not 'connected' at turn-1 init (watch for 0 sense calls)" >&2
      fi
    fi
    echo "[session]   turn $((i+1))/$NSTEPS rc=$rc cum_wall=${total_wall}s" >&2
  done

  # Loud post-run guard: the sense arm MUST have used Sense, else it silently
  # degraded to grep (the bug that invalidated run #1). Surfaces it instead of
  # quietly reporting a meaningless tie.
  if [[ "$tool" == sense ]]; then
    nmcp=$(grep -oE '"name":"mcp__sense__[a-z_]+"' "$out/transcript.json" | wc -l | tr -d ' ')
    if [[ "${nmcp:-0}" -gt 0 ]]; then echo "[session]   sense used $nmcp Sense tool calls (valid)" >&2
    else echo "[session]   *** INVALID: sense arm made 0 Sense tool calls — ran as grep ***" >&2; fi
  fi

  commit=$(git -C "$repo_dir" rev-parse --short HEAD 2>/dev/null || echo "")
  ver=""; [[ "$tool" == sense ]] && ver="$SVER"
  python3 - "$tool" "$REPO" "$SCEN_NAME" "$total_wall" "$MODEL" "$commit" "$ver" > "$out/run_meta.json" <<'PY'
import json, sys
tool, repo, scen, wall, model, commit, ver = sys.argv[1:8]
print(json.dumps({
    "tool": tool, "repo": repo, "scenario": scen,
    "wall_time_seconds": int(wall), "model": model,
    "repo_commit": commit or None, "tool_version": ver or None,
    "auth_mode": "subscription_cli", "mode": "session_multiturn",
}, indent=2))
PY
done

SJ=(--tool "$TOOLS_CSV" --repo "$REPO")
bash "$BENCH_DIR/score.sh"  "${SJ[@]}"
bash "$BENCH_DIR/judge.sh"  "${SJ[@]}" --via-cli
# Per-model report (md + json) for this model root, then refresh the vertical's
# cross-model matrix so every entry point keeps the tracked reports current.
bash "$BENCH_DIR/report.sh" --md
bash "$BENCH_DIR/report.sh" --json
[ -n "${VERTICAL:-}" ] && { bash "$BENCH_DIR/drivers/report-matrix.sh" >/dev/null 2>&1 || echo "[warn] matrix refresh failed" >&2; }
echo "[session] done — see bench/results/{${TOOLS_CSV}}/$REPO/" >&2
