#!/usr/bin/env bash
# opencode-run.sh runs the Rails-vertical bench through the opencode agent,
# driving Ollama-cloud models (deepseek-v4-pro, etc.). Replaces the old path
# that pointed the Claude CLI at the Ollama daemon's Anthropic-compatible
# endpoint, which drove the cloud models so poorly they ignored Sense (2 sense
# vs 97 native calls). opencode has a native, authed `ollama-cloud` provider
# and native MCP support, so the model actually uses the tools.
#
# Single-prompt over the 7-step scenario (the trustworthy path): renders all
# steps into one prompt, runs `opencode run --format json`, normalizes the
# JSONL into the canonical transcript scorer.py reads (via
# lib/parse-opencode-result.py), then score -> judge (--via-cli) -> report.
# Writes to bench/results/{baseline,sense}/<repo>/ so the existing
# score/judge/report/snapshot pipeline runs unchanged.
#
#   bash bench/opencode-run.sh --tool baseline,sense --repo ruby_llm
#   bash bench/opencode-run.sh --repo discourse --model deepseek-v4-pro:cloud  # campaign id, auto-mapped
#
# Sense via MCP (primary) + CLI (fallback), both counted in channels.json. The
# sense arm gets opencode's canonical surface from `sense setup --tools opencode`
# (opencode.json registering the Sense MCP server + AGENTS.md + .opencode/skills/,
# the parallel to Claude's CLAUDE.md + .claude/skills/), plus `sense` on PATH.
# The baseline arm gets none of it and runs with the sense binary's dir stripped
# from PATH, so it reaches Sense by neither channel.
#
# NOTE on the cold start: opencode + a local MCP server is SLOW to first output
# (it spawns and initializes the MCP server before the first streamed event), so
# give the run room. An earlier "opencode hangs on MCP" diagnosis was wrong: it
# was premature kills at 35-60s. Verified end to end: deepseek-v4-pro and -flash
# both call sense_sense_graph and return file-pinned answers (ruby_llm + sense).
#
# Prereqs: clones at $SENSE_BENCH_ROOT/{baseline,sense}/<repo>; sense arm already
# `sense scan`-ed; opencode authed for ollama-cloud (`opencode providers list`);
# `sense` on PATH. Judge stays claude-opus-4-7 on the Claude subscription.

set -uo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
SCENARIOS_DIR="$BENCH_DIR/scenarios"
RESULTS_DIR="$BENCH_DIR/results"
LIB_DIR="$BENCH_DIR/lib"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"

TOOLS_CSV="baseline,sense"; REPO=""; MODEL="ollama-cloud/deepseek-v4-pro"
SESSION_TIMEOUT=""; KEEP_RAW=0
while [[ $# -gt 0 ]]; do case "$1" in
  --tool) TOOLS_CSV="$2"; shift 2;;
  --repo) REPO="$2"; shift 2;;
  --model) MODEL="$2"; shift 2;;
  --timeout) SESSION_TIMEOUT="$2"; shift 2;;
  --keep-raw) KEEP_RAW=1; shift;;
  -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0;;
  *) echo "unknown arg: $1" >&2; exit 1;;
esac; done
[[ -n "$REPO" ]] || { echo "need --repo" >&2; exit 1; }

# Accept the campaign's colon id (deepseek-v4-pro:cloud) and map it to opencode's
# native provider id (ollama-cloud/deepseek-v4-pro). Pass-through if already in
# provider/model form.
case "$MODEL" in
  */*) : ;;                                   # already provider/model
  *:cloud) MODEL="ollama-cloud/${MODEL%:cloud}" ;;
  *) MODEL="ollama-cloud/$MODEL" ;;
esac

command -v opencode >/dev/null || { echo "opencode CLI not found in PATH" >&2; exit 1; }
command -v sense >/dev/null || { echo "sense not found in PATH (needed for the sense arm)" >&2; exit 1; }
unset ANTHROPIC_API_KEY BENCHMARK_ANTHROPIC_API_KEY

SENSE_BIN_DIR="$(dirname "$(command -v sense)")"
SCRUBBED_PATH="$(printf '%s' "$PATH" | tr ':' '\n' | grep -vFx "$SENSE_BIN_DIR" | paste -sd: -)"

SCEN="$SCENARIOS_DIR/$REPO.yaml"
[[ -f "$SCEN" ]] || { echo "no scenario $SCEN" >&2; exit 1; }
SCEN_NAME=$(python3 -c "import yaml;print(yaml.safe_load(open('$SCEN'))['name'])")
PROMPT=$(python3 "$LIB_DIR/scenario.py" "$SCEN" --prompt)
SVER="$(sense --version 2>/dev/null | head -1 || echo '')"

if [[ -n "$SESSION_TIMEOUT" ]]; then SECS="$SESSION_TIMEOUT"; else
  SECS=$(python3 -c "import sys;sys.path.insert(0,'$LIB_DIR');from scorer import TIME_CEILINGS,DEFAULT_TIME_CEILING;print(max(600,TIME_CEILINGS.get('$REPO',DEFAULT_TIME_CEILING)))")
fi

# Real wall-clock ceiling. macOS ships no `timeout`; fall back to a pure-bash
# watchdog so a genuinely stuck run is killed (rc 124) instead of blocking the
# sweep. Mirrors bench-sense-local.sh.
bash_timeout() {
  local secs=$1; shift
  "$@" & local pid=$!
  ( sleep "$secs"; kill -TERM "$pid" 2>/dev/null; sleep 5; kill -KILL "$pid" 2>/dev/null ) & local wd=$!
  local rc=0; wait "$pid" 2>/dev/null || rc=$?
  kill "$wd" 2>/dev/null; wait "$wd" 2>/dev/null
  [[ $rc -eq 143 ]] && rc=124
  return $rc
}
if command -v timeout >/dev/null; then TO=(timeout "$SECS")
elif command -v gtimeout >/dev/null; then TO=(gtimeout "$SECS")
else TO=(bash_timeout "$SECS"); fi

IFS=',' read -ra TOOLS <<< "$TOOLS_CSV"
for tool in "${TOOLS[@]}"; do
  repo_dir="$SENSE_BENCH_ROOT/$tool/$REPO"
  [[ -d "$repo_dir/.git" ]] || { echo "[opencode] SKIP $tool: clone missing at $repo_dir" >&2; continue; }
  out="$RESULTS_DIR/$tool/$REPO"; mkdir -p "$out"
  echo "[opencode] $tool/$REPO model=$MODEL timeout=${SECS}s" >&2

  # Clean slate, then for the sense arm write opencode's canonical Sense surface
  # (opencode.json MCP server + AGENTS.md + .opencode/skills/) via `sense setup`.
  # The sense binary stays on PATH (CLI fallback = dual channel). The baseline
  # arm gets none of it and a PATH with the sense dir stripped.
  rm -f "$repo_dir/opencode.json" "$repo_dir/AGENTS.md"; rm -rf "$repo_dir/.opencode"
  if [[ "$tool" == sense ]]; then
    ( cd "$repo_dir" && sense setup --tools opencode >/dev/null 2>&1 ) \
      || echo "[opencode]   WARN: sense setup --tools opencode failed" >&2
    run_path="$PATH"
  else
    run_path="$SCRUBBED_PATH"
  fi

  raw="$out/opencode-raw.jsonl"
  start=$(date +%s)
  ( cd "$repo_dir" && PATH="$run_path" "${TO[@]}" \
      opencode run --format json -m "$MODEL" --dir "$repo_dir" \
      --dangerously-skip-permissions "$PROMPT" ) > "$raw" 2> "$out/opencode.log"
  rc=$?
  wall=$(( $(date +%s) - start ))

  rm -f "$repo_dir/opencode.json" "$repo_dir/AGENTS.md"; rm -rf "$repo_dir/.opencode"
  git -C "$repo_dir" checkout -- . 2>/dev/null || true   # revert any stray edits (tracked only)

  python3 "$LIB_DIR/parse-opencode-result.py" "$raw" --channels-json "$out/channels.json" \
      > "$out/transcript.json" 2>> "$out/opencode.log" || echo "[opencode] parse failed ($tool)" >&2
  cp "$out/opencode.log" "$out/claude.log" 2>/dev/null || true
  [[ "$KEEP_RAW" == 1 ]] || rm -f "$raw"

  nmcp=$(python3 -c "import json;print(json.load(open('$out/channels.json'))['channels']['mcp_sense'])" 2>/dev/null || echo 0)
  ncli=$(python3 -c "import json;print(json.load(open('$out/channels.json'))['channels']['cli_sense'])" 2>/dev/null || echo 0)
  if [[ "$tool" == sense ]]; then
    if [[ $((nmcp + ncli)) -gt 0 ]]; then echo "[opencode]   sense used: mcp=$nmcp cli=$ncli (valid)" >&2
    else echo "[opencode]   *** INVALID: sense arm reached Sense 0 times (mcp=0 cli=0) ***" >&2; fi
  fi

  commit=$(git -C "$repo_dir" rev-parse --short HEAD 2>/dev/null || echo "")
  ver=""; [[ "$tool" == sense ]] && ver="$SVER"
  python3 - "$tool" "$REPO" "$SCEN_NAME" "$wall" "$MODEL" "$commit" "$ver" "$rc" > "$out/run_meta.json" <<'PY'
import json, sys
tool, repo, scen, wall, model, commit, ver, rc = sys.argv[1:9]
meta = {
    "tool": tool, "repo": repo, "scenario": scen,
    "wall_time_seconds": int(wall), "model": model,
    "repo_commit": commit or None, "tool_version": ver or None,
    "harness": "opencode", "provider": "ollama-cloud",
    "auth_mode": "opencode_cli", "mode": "single_prompt",
    "opencode_exit_code": int(rc),
    "cost_usd_note": "ollama-cloud bills off-platform; per-token cost left null",
}
if int(rc) != 0:
    meta["error"] = "opencode_session_failed"
print(json.dumps(meta, indent=2))
PY
  echo "[opencode]   $tool rc=$rc wall=${wall}s" >&2
done

SJ=(--tool "$TOOLS_CSV" --repo "$REPO")
bash "$BENCH_DIR/score.sh"  "${SJ[@]}"
bash "$BENCH_DIR/judge.sh"  "${SJ[@]}" --via-cli
bash "$BENCH_DIR/report.sh" --md
echo "[opencode] done, see bench/results/{${TOOLS_CSV}}/$REPO/ (channels.json per arm)" >&2
