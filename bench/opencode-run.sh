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
# sense arm gets opencode's canonical surface from full `sense setup`
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
# Resolves SCENARIOS_DIR + RESULTS_DIR for the global or VERTICAL bench.
source "$BENCH_DIR/lib/bench-paths.sh"
LIB_DIR="$BENCH_DIR/lib"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"

TOOLS_CSV="baseline,sense"; REPO=""; MODEL="ollama-cloud/deepseek-v4-pro"
SESSION_TIMEOUT=""; KEEP_RAW=0
# Stability knobs (ollama-cloud over opencode is flaky). See the watchdog below.
OPENCODE_MAX_SECS="${OPENCODE_MAX_SECS:-1200}"     # hard ceiling floor (was a flat 600 that killed slow-but-working sense runs)
OPENCODE_FIRST_GRACE="${OPENCODE_FIRST_GRACE:-240}" # allow this long for the FIRST streamed byte (MCP cold start); 0 bytes past it = a hang
OPENCODE_STALL_IDLE="${OPENCODE_STALL_IDLE:-150}"   # after output starts, kill only if the stream goes silent this long (stuck mid-run)
OPENCODE_RETRIES="${OPENCODE_RETRIES:-1}"           # extra attempts for a TRUE no-output hang (total attempts = retries+1)
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
  SECS=$(python3 -c "import sys;sys.path.insert(0,'$LIB_DIR');from scorer import TIME_CEILINGS,DEFAULT_TIME_CEILING;print(max($OPENCODE_MAX_SECS,TIME_CEILINGS.get('$REPO',DEFAULT_TIME_CEILING)))")
fi

# Stall-aware watchdog. A flat wall-clock cap GUILLOTINES slow-but-working sense
# runs: the sense arm streams more tool calls + steps, so on heavy scenarios it
# legitimately needs >600s and was being killed mid-answer (scoring 0) while the
# faster baseline finished -- a fairness bug. opencode --format json streams one
# JSON part per line as work happens, so we can tell "working but slow" (the raw
# file keeps growing) from a "cold-start hang" (0 bytes, never starts):
#   - FIRST_GRACE: a true hang writes nothing -> kill fast (saves quota), retry.
#   - STALL_IDLE : after output starts, kill only if the stream is silent this
#     long (genuinely stuck), NOT merely slow.
#   - SECS       : absolute hard cap so nothing blocks the sweep forever.
# rc: 0 ok · 124 hard-cap · 125 stalled mid-run · 126 no first output (hang).
fsize() { stat -f%z "$1" 2>/dev/null || stat -c%s "$1" 2>/dev/null || echo 0; }
run_guarded() {  # $1 = raw file (absolute); $2.. = command
  local raw="$1"; shift
  : > "$raw"
  "$@" >> "$raw" 2>> "$LOGFILE" & local pid=$!
  local start now sz last_sz=0 last_change reason=0 elapsed idle
  start=$(date +%s); last_change=$start
  while kill -0 "$pid" 2>/dev/null; do
    sleep 10
    now=$(date +%s); sz=$(fsize "$raw")
    [ "$sz" -gt "$last_sz" ] && { last_sz=$sz; last_change=$now; }
    elapsed=$(( now - start )); idle=$(( now - last_change ))
    if   [ "$elapsed" -ge "$SECS" ]; then reason=124; break
    elif [ "$last_sz" -eq 0 ] && [ "$elapsed" -ge "$OPENCODE_FIRST_GRACE" ]; then reason=126; break
    elif [ "$last_sz" -gt 0 ] && [ "$idle" -ge "$OPENCODE_STALL_IDLE" ]; then reason=125; break
    fi
  done
  if [ "$reason" -ne 0 ]; then
    kill -TERM "$pid" 2>/dev/null; sleep 3; kill -KILL "$pid" 2>/dev/null
    wait "$pid" 2>/dev/null; return "$reason"
  fi
  wait "$pid" 2>/dev/null; return $?
}

IFS=',' read -ra TOOLS <<< "$TOOLS_CSV"
for tool in "${TOOLS[@]}"; do
  repo_dir="$SENSE_BENCH_ROOT/$tool/$REPO"
  [[ -d "$repo_dir/.git" ]] || { echo "[opencode] SKIP $tool: clone missing at $repo_dir" >&2; continue; }
  out="$RESULTS_DIR/$tool/$REPO"; mkdir -p "$out"
  echo "[opencode] $tool/$REPO model=$MODEL timeout=${SECS}s" >&2

  # Clean slate, then for the sense arm write the full Sense surface via
  # `sense setup` (no --tools): every detected tool is configured, incl.
  # opencode's opencode.json MCP server + AGENTS.md + .opencode/skills/. We do
  # NOT scope to --tools opencode — the scoped form is what silently left the
  # codex arm un-set-up; each tool reads only its own file with identical
  # guidance text, so full setup never cross-contaminates. The sense binary
  # stays on PATH (CLI fallback = dual channel). The baseline arm gets none of
  # it and a PATH with the sense dir stripped.
  rm -f "$repo_dir/opencode.json" "$repo_dir/AGENTS.md"; rm -rf "$repo_dir/.opencode"
  if [[ "$tool" == sense ]]; then
    ( cd "$repo_dir" && sense setup >/dev/null 2>&1 ) \
      || echo "[opencode]   WARN: sense setup failed" >&2
    run_path="$PATH"
  else
    run_path="$SCRUBBED_PATH"
  fi

  raw="$out/opencode-raw.jsonl"; LOGFILE="$out/opencode.log"; : > "$LOGFILE"
  attempts=$((OPENCODE_RETRIES + 1)); start=$(date +%s); rc=0; otok=0
  for attempt in $(seq 1 "$attempts"); do
    git -C "$repo_dir" checkout -- . 2>/dev/null || true   # reset tracked edits between attempts (keeps untracked sense surface)
    ( cd "$repo_dir" && PATH="$run_path" run_guarded "$raw" \
        opencode run --format json -m "$MODEL" --dir "$repo_dir" \
        --dangerously-skip-permissions "$PROMPT" )
    rc=$?
    python3 "$LIB_DIR/parse-opencode-result.py" "$raw" --channels-json "$out/channels.json" \
        > "$out/transcript.json" 2>> "$LOGFILE" || echo "[opencode] parse failed ($tool)" >&2
    otok=$(python3 -c "
import json
t=0
try:
  for l in open('$out/transcript.json'):
    l=l.strip()
    if not l: continue
    try: d=json.loads(l)
    except: continue
    u=d.get('usage') or {}; t+=int(u.get('output_tokens') or 0)
except FileNotFoundError: pass
print(t)" 2>/dev/null || echo 0)
    # Accept any run that produced a real answer (rc 0, or output even if capped:
    # a slow sense run that streamed 14k tokens is a valid, if truncated, datum,
    # NOT a failure). Retry ONLY a true no-output hang (rc!=0 AND 0 tokens).
    if [ "$rc" -eq 0 ] || [ "${otok:-0}" -gt 0 ]; then
      [ "$attempt" -gt 1 ] && echo "[opencode]   recovered on attempt $attempt (rc=$rc, out_tok=$otok)" >&2
      break
    fi
    echo "[opencode]   attempt $attempt/$attempts: no output (rc=$rc, 0 tok) -- $([ "$attempt" -lt "$attempts" ] && echo retrying || echo 'giving up')" >&2
  done
  wall=$(( $(date +%s) - start ))

  rm -f "$repo_dir/opencode.json" "$repo_dir/AGENTS.md"; rm -rf "$repo_dir/.opencode"
  git -C "$repo_dir" checkout -- . 2>/dev/null || true   # revert any stray edits (tracked only)

  cp "$LOGFILE" "$out/claude.log" 2>/dev/null || true
  [[ "$KEEP_RAW" == 1 ]] || rm -f "$raw"

  nmcp=$(python3 -c "import json;print(json.load(open('$out/channels.json'))['channels']['mcp_sense'])" 2>/dev/null || echo 0)
  ncli=$(python3 -c "import json;print(json.load(open('$out/channels.json'))['channels']['cli_sense'])" 2>/dev/null || echo 0)
  if [[ "$tool" == sense ]]; then
    if [[ $((nmcp + ncli)) -gt 0 ]]; then echo "[opencode]   sense used: mcp=$nmcp cli=$ncli (valid)" >&2
    else echo "[opencode]   *** INVALID: sense arm reached Sense 0 times (mcp=0 cli=0) ***" >&2; fi
  fi

  commit=$(git -C "$repo_dir" rev-parse --short HEAD 2>/dev/null || echo "")
  ver=""; [[ "$tool" == sense ]] && ver="$SVER"
  python3 - "$tool" "$REPO" "$SCEN_NAME" "$wall" "$MODEL" "$commit" "$ver" "$rc" "$attempts" "$otok" > "$out/run_meta.json" <<'PY'
import json, sys
tool, repo, scen, wall, model, commit, ver, rc, attempts, otok = sys.argv[1:11]
rc = int(rc); otok = int(otok)
# Classify the watchdog exit so the contaminated-vs-real distinction is legible
# downstream (124 hard cap / 125 stalled / 126 cold-start hang).
KIND = {0: None, 124: "hard_cap_timeout", 125: "stalled_midrun", 126: "no_first_output_hang"}
meta = {
    "tool": tool, "repo": repo, "scenario": scen,
    "wall_time_seconds": int(wall), "model": model,
    "repo_commit": commit or None, "tool_version": ver or None,
    "harness": "opencode", "provider": "ollama-cloud",
    "auth_mode": "opencode_cli", "mode": "single_prompt",
    "opencode_exit_code": rc, "attempts": int(attempts), "output_tokens": otok,
    "cost_usd_note": "ollama-cloud bills off-platform; per-token cost left null",
}
kind = KIND.get(rc, "opencode_session_failed")
if kind:
    meta["watchdog_kind"] = kind
# Only a TRUE no-output hang is a failed run; a capped/stalled run that still
# streamed tokens is a valid (truncated) datum, not a 0.
if rc != 0 and otok == 0:
    meta["error"] = "opencode_session_failed"
elif rc != 0:
    meta["note"] = f"watchdog stopped ({kind}) but produced {otok} output tokens; kept as a truncated-but-valid run"
print(json.dumps(meta, indent=2))
PY
  echo "[opencode]   $tool rc=$rc wall=${wall}s attempts=$attempts out_tok=$otok" >&2
done

SJ=(--tool "$TOOLS_CSV" --repo "$REPO")
bash "$BENCH_DIR/score.sh"  "${SJ[@]}"
bash "$BENCH_DIR/judge.sh"  "${SJ[@]}" --via-cli
bash "$BENCH_DIR/report.sh" --md
echo "[opencode] done, see bench/results/{${TOOLS_CSV}}/$REPO/ (channels.json per arm)" >&2
