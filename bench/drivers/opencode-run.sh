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
#   bash bench/drivers/opencode-run.sh --tool baseline,sense --repo ruby_llm
#   bash bench/drivers/opencode-run.sh --repo discourse --model deepseek-v4-pro:cloud  # campaign id, auto-mapped
#   bash bench/drivers/opencode-run.sh --repo discourse --model ollama-cloud/qwen3-coder-next  # Qwen coder arm
#   bash bench/drivers/opencode-run.sh --repo discourse --model kimi-for-coding/k2p7           # Kimi for Coding arm
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
# `sense` on PATH. Judge stays claude-sonnet-4-6 on the Claude subscription.

set -uo pipefail

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
# Resolves SCENARIOS_DIR + RESULTS_DIR for the global or VERTICAL bench.
source "$BENCH_DIR/lib/bench-paths.sh"
# Subscription-throttle pacing for this METERED arm (default-on; BENCH_THROTTLE_PACING=0
# = exact pre-pacing behavior). Sourced AFTER bench-paths so the health log can default
# under RESULTS_DIR. The claude/opus runner never sources this.
source "$BENCH_DIR/lib/throttle-pacing.sh"
LIB_DIR="$BENCH_DIR/lib"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"

TOOLS_CSV="baseline,sense"; REPO=""; MODEL="kimi-for-coding/k2p7"
SESSION_TIMEOUT=""; KEEP_RAW=0
# Stability knobs (cloud/subscription providers over opencode can be flaky). See the watchdog below.
# Record whether the caller pinned these BEFORE defaulting, so the metered-arm
# bump below only fires when they were left at the default (an explicit override wins).
OPENCODE_MAX_SECS_SET="${OPENCODE_MAX_SECS+1}"
OPENCODE_STALL_IDLE_SET="${OPENCODE_STALL_IDLE+1}"
OPENCODE_MAX_SECS="${OPENCODE_MAX_SECS:-1200}"     # hard ceiling floor (was a flat 600 that killed slow-but-working sense runs)
OPENCODE_FIRST_GRACE="${OPENCODE_FIRST_GRACE:-240}" # allow this long for the FIRST streamed byte (MCP cold start); 0 bytes past it = a hang
OPENCODE_STALL_IDLE="${OPENCODE_STALL_IDLE:-150}"   # after output starts, kill only if the stream goes silent this long (stuck mid-run)
OPENCODE_RETRIES="${OPENCODE_RETRIES:-1}"           # extra attempts for a TRUE no-output hang (total attempts = retries+1)
OPENCODE_MIN_ANSWER_CHARS="${OPENCODE_MIN_ANSWER_CHARS:-200}" # a run whose final assistant text is shorter than this is a
                                                    # truncated/empty-stream artifact, not a real answer: retry it, and flag
                                                    # it as invalid if it stays empty (the audit answers run ~4000+ chars, so
                                                    # 200 is far below any real answer yet catches the 0/94-char degenerate runs)
OPENCODE_OFFLOAD_DETECT="${OPENCODE_OFFLOAD_DETECT:-1}" # answer-offload gate (the gitlabhq/Kimi+Qwen failure mode): the model
                                                    # writes its real audit into a scratch file ($TMPDIR/opencode/{final,*_audit,
                                                    # compact}.json or the repo tree) and returns a short POINTER stub that can
                                                    # clear the char gate yet scores 0.0. We isolate the run's TMPDIR, then flag a
                                                    # run as offloaded if a *_audit/final*/compact*.json artifact appears OR a
                                                    # short answer points at a written .json file. Treated like a truncation:
                                                    # retried, then flagged error=answer_offloaded_to_file. Set 0 to disable.
OPENCODE_OFFLOAD_MAX_CHARS="${OPENCODE_OFFLOAD_MAX_CHARS:-4000}" # a "pointer stub" is short; a real inline audit runs ~4000+ chars.
                                                    # The content-signal half of the gate only fires below this length so a long,
                                                    # complete inline answer that merely mentions a .json path is never nuked.
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

# Metered Kimi-for-Coding throttles SILENTLY for minutes at the heavy final-
# synthesis turn; the default 150s stall watchdog guillotines that recoverable
# cooldown -> empty_final_answer (proven: native Kimi waits it out and converges).
# Give this arm a longer stall tolerance + hard cap unless the caller pinned them.
case "$MODEL" in
  *kimi*)
    [ -z "$OPENCODE_STALL_IDLE_SET" ] && OPENCODE_STALL_IDLE=600
    [ -z "$OPENCODE_MAX_SECS_SET" ]   && OPENCODE_MAX_SECS=3000
    ;;
esac

command -v opencode >/dev/null || { echo "opencode CLI not found in PATH" >&2; exit 1; }
command -v sense >/dev/null || { echo "sense not found in PATH (needed for the sense arm)" >&2; exit 1; }
unset ANTHROPIC_API_KEY BENCHMARK_ANTHROPIC_API_KEY

SENSE_BIN_DIR="$(dirname "$(command -v sense)")"
SCRUBBED_PATH="$(printf '%s' "$PATH" | tr ':' '\n' | grep -vFx "$SENSE_BIN_DIR" | paste -sd: -)"

# Per-repo lock so two sessions can bench DIFFERENT repos concurrently and only
# ever serialize on the SAME repo's clones+results. If a sweep parent already holds
# this repo's lock (exported BENCH_PACE_LOCK_HELD), this is a no-op. Released on exit.
pace_lock_acquire "repo-$REPO"

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
# Optional sense-first ordering (BENCH_SENSE_FIRST=1): run the heavier sense arm
# into the fresher window. Default keeps the input order. 3.2-safe (no mapfile).
ORDERED=(); while IFS= read -r _t; do [ -n "$_t" ] && ORDERED+=("$_t"); done < <(pace_order_tools "${TOOLS[@]}")
TOOLS=("${ORDERED[@]}")
arm_idx=0
for tool in "${TOOLS[@]}"; do
  repo_dir="$SENSE_BENCH_ROOT/$tool/$REPO"
  [[ -d "$repo_dir/.git" ]] || { echo "[opencode] SKIP $tool: clone missing at $repo_dir" >&2; continue; }
  # Inter-arm spacing so the second arm starts in a less-drained window.
  [ "$arm_idx" -gt 0 ] && pace_sleep "$OPENCODE_PACE_SECONDS" "between arms (next $tool/$REPO)"
  arm_idx=$(( arm_idx + 1 ))
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

  # Enable opencode tool-output pruning + a larger compaction buffer for BOTH
  # arms (uniform = fair). prune:true drops stale tool outputs from context so the
  # heavy final-synthesis turn stays light enough to clear a metered throttle (the
  # Kimi empty_final_answer fix). Merges into the sense arm's MCP opencode.json;
  # creates a compaction-only one for the baseline (which otherwise has none).
  python3 - "$repo_dir/opencode.json" <<'PY'
import json, os, sys
p = sys.argv[1]
cfg = {}
if os.path.exists(p):
    try:
        with open(p) as f:
            cfg = json.load(f)
    except (json.JSONDecodeError, OSError):
        cfg = {}
cfg.setdefault("$schema", "https://opencode.ai/config.json")
cfg["compaction"] = {"auto": True, "prune": True, "reserved": 30000}
with open(p, "w") as f:
    json.dump(cfg, f, indent=2)
PY

  raw="$out/opencode-raw.jsonl"; LOGFILE="$out/opencode.log"; : > "$LOGFILE"
  # Isolated scratch for the offload gate: point the run's TMPDIR here so the model's
  # "write my audit to a file" behavior lands somewhere we can deterministically scan
  # (opencode + the model's bash tool both honor TMPDIR). Recreated fresh per attempt.
  run_scratch="$out/run-scratch"
  attempts=$((OPENCODE_RETRIES + 1)); start=$(date +%s); rc=0; otok=0; achars=0; offload=0
  for attempt in $(seq 1 "$attempts"); do
    git -C "$repo_dir" checkout -- . 2>/dev/null || true   # reset tracked edits between attempts (keeps untracked sense surface)
    # Clear offload artifacts a prior attempt/run wrote into the repo tree: they are
    # UNTRACKED (git checkout won't remove them) and would false-positive this
    # attempt's offload scan. Names match the detector; the sense surface
    # (opencode.json/AGENTS.md/.opencode) never matches, so it is preserved.
    [ "$OPENCODE_OFFLOAD_DETECT" = 1 ] && find "$repo_dir" -maxdepth 2 -type f \
      \( -iname '*audit*.json' -o -iname '*final*.json' -o -iname '*compact*.json' \) -delete 2>/dev/null || true
    rm -rf "$run_scratch"; mkdir -p "$run_scratch"         # fresh offload-scratch per attempt
    ( cd "$repo_dir" && export PATH="$run_path" TMPDIR="$run_scratch" && run_guarded "$raw" \
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
    # Inspect the FINAL assistant answer, built exactly like the scorer's
    # read_answer_text (concatenated assistant text blocks). Emits two values:
    #   achars = answer length. A run can stream tokens (otok>0) yet leave an
    #            empty/near-empty final answer when the stream truncates -- a
    #            failed datum, not a real 0.0.
    #   perr   = 1 if the "answer" is actually an ollama-cloud provider error
    #            (the 94-char `{"error":{"type":"llm_call_failed",...Operation
    #            not allowed...}}` blob = a rate-limit/session cap, NOT a model
    #            answer). Classified separately so cap hits are legible vs plain
    #            truncations.
    read achars perr < <(python3 -c "
import json
parts=[]
try:
  for l in open('$out/transcript.json'):
    l=l.strip()
    if not l: continue
    try: d=json.loads(l)
    except: continue
    e=d.get('event', d)
    if e.get('type') != 'assistant': continue
    for b in e.get('message', {}).get('content', []):
      if b.get('type') == 'text' and b.get('text'): parts.append(b['text'])
except FileNotFoundError: pass
ans='\n'.join(parts)
perr = 1 if ('llm_call_failed' in ans or 'Operation not allowed' in ans) else 0
print(len(ans), perr)" 2>/dev/null || echo "0 0")
    achars="${achars:-0}"; perr="${perr:-0}"
    # Offload gate: did the model write its audit to a scratch file and return a
    # pointer stub? Two signals -- (a) a *_audit/final*/compact*.json artifact in the
    # isolated TMPDIR or the repo tree, or (b) a SHORT answer (< OFFLOAD_MAX_CHARS)
    # that points at a written .json file. Either => offloaded => retry/flag.
    offload=0
    if [ "$OPENCODE_OFFLOAD_DETECT" = 1 ]; then
      offload=$(python3 - "$out/transcript.json" "$run_scratch" "$repo_dir" "$achars" "$OPENCODE_OFFLOAD_MAX_CHARS" <<'PY' 2>/dev/null || echo 0
import json, os, re, sys, glob
tpath, scratch, repo_dir, achars, maxchars = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5])
# (a) file signal: model-chosen audit/final/compact .json names in the scratch or repo tree.
NAME = re.compile(r'(audit|final|compact|merge_request_audit).*\.json$', re.I)
def has_artifact(root, maxdepth):
    if not os.path.isdir(root): return False
    root = root.rstrip('/')
    base_depth = root.count(os.sep)
    for dp, _dn, fns in os.walk(root):
        if dp.count(os.sep) - base_depth > maxdepth: continue
        for fn in fns:
            if NAME.search(fn): return True
    return False
file_sig = has_artifact(scratch, 4) or has_artifact(repo_dir, 1)
# (b) content signal: a SHORT answer that says it wrote/saved its output to a .json file.
parts = []
try:
    for l in open(tpath):
        l = l.strip()
        if not l: continue
        try: d = json.loads(l)
        except Exception: continue
        e = d.get('event', d)
        if e.get('type') != 'assistant': continue
        for b in e.get('message', {}).get('content', []):
            if b.get('type') == 'text' and b.get('text'): parts.append(b['text'])
except FileNotFoundError: pass
ans = '\n'.join(parts)
POINTER = re.compile(r'(written|wrote|saved|stored|output(?:ted)?|see|created|generated)\b[^\n]{0,60}\.json', re.I)
content_sig = achars < maxchars and bool(POINTER.search(ans))
print(1 if (file_sig or content_sig) else 0)
PY
)
      offload="${offload:-0}"
    fi
    # Accept a run only if it produced a REAL answer: tokens streamed AND a
    # final answer of usable length AND not a provider error. Retry everything
    # else: a true no-output hang (otok=0), an empty/truncated answer (achars <
    # min), or a provider cap error (perr=1) -- all were previously scored 0.0.
    if [ "${otok:-0}" -gt 0 ] && [ "${achars:-0}" -ge "$OPENCODE_MIN_ANSWER_CHARS" ] && [ "$perr" -eq 0 ] && [ "$offload" -eq 0 ]; then
      [ "$attempt" -gt 1 ] && echo "[opencode]   recovered on attempt $attempt (rc=$rc, out_tok=$otok, answer_chars=$achars)" >&2
      break
    fi
    if [ "$perr" -eq 1 ]; then
      reason="provider error (llm_call_failed / 'Operation not allowed' -- likely an ollama-cloud cap)"
    elif [ "$offload" -eq 1 ]; then
      reason="answer offloaded to file (wrote *_audit/final*/compact*.json + returned a pointer stub; scores 0)"
    elif [ "${otok:-0}" -gt 0 ] && [ "${achars:-0}" -lt "$OPENCODE_MIN_ANSWER_CHARS" ]; then
      reason="empty/truncated answer (out_tok=$otok, answer_chars=$achars < $OPENCODE_MIN_ANSWER_CHARS)"
    else
      reason="no output (rc=$rc, 0 tok)"
    fi
    echo "[opencode]   attempt $attempt/$attempts: $reason -- $([ "$attempt" -lt "$attempts" ] && echo retrying || echo 'giving up')" >&2
    # Pace the retry by failure class. A provider cap (perr=1) or a truncated
    # answer (tokens streamed but the answer is below min) means the window is
    # drained -- back off LONG so the next attempt lands in a fresher window. A
    # TRUE no-output hang (0 tokens) is not throttle-related: retry it fast (the
    # watchdog already burned time), so skip the backoff.
    if [ "$attempt" -lt "$attempts" ]; then
      if [ "$perr" -eq 1 ] || { [ "${otok:-0}" -gt 0 ] && [ "${achars:-0}" -lt "$OPENCODE_MIN_ANSWER_CHARS" ]; }; then
        pace_backoff "$attempt" "throttle/truncation before retry"
      fi
    fi
  done
  wall=$(( $(date +%s) - start ))

  rm -f "$repo_dir/opencode.json" "$repo_dir/AGENTS.md"; rm -rf "$repo_dir/.opencode"
  git -C "$repo_dir" checkout -- . 2>/dev/null || true   # revert any stray edits (tracked only)

  cp "$LOGFILE" "$out/claude.log" 2>/dev/null || true
  [[ "$KEEP_RAW" == 1 ]] || rm -f "$raw"
  [[ "$KEEP_RAW" == 1 ]] || rm -rf "$run_scratch"   # offload-scratch: keep only with --keep-raw

  nmcp=$(python3 -c "import json;print(json.load(open('$out/channels.json'))['channels']['mcp_sense'])" 2>/dev/null || echo 0)
  ncli=$(python3 -c "import json;print(json.load(open('$out/channels.json'))['channels']['cli_sense'])" 2>/dev/null || echo 0)
  if [[ "$tool" == sense ]]; then
    if [[ $((nmcp + ncli)) -gt 0 ]]; then echo "[opencode]   sense used: mcp=$nmcp cli=$ncli (valid)" >&2
    else echo "[opencode]   *** INVALID: sense arm reached Sense 0 times (mcp=0 cli=0) ***" >&2; fi
  fi

  commit=$(git -C "$repo_dir" rev-parse --short HEAD 2>/dev/null || echo "")
  ver=""; [[ "$tool" == sense ]] && ver="$SVER"
  python3 - "$tool" "$REPO" "$SCEN_NAME" "$wall" "$MODEL" "$commit" "$ver" "$rc" "$attempts" "$otok" "$achars" "$OPENCODE_MIN_ANSWER_CHARS" "$perr" "$offload" > "$out/run_meta.json" <<'PY'
import json, sys
tool, repo, scen, wall, model, commit, ver, rc, attempts, otok, achars, min_chars, perr, offload = sys.argv[1:15]
rc = int(rc); otok = int(otok); achars = int(achars); min_chars = int(min_chars); perr = int(perr); offload = int(offload)
# Classify the watchdog exit so the contaminated-vs-real distinction is legible
# downstream (124 hard cap / 125 stalled / 126 cold-start hang).
KIND = {0: None, 124: "hard_cap_timeout", 125: "stalled_midrun", 126: "no_first_output_hang"}
meta = {
    "tool": tool, "repo": repo, "scenario": scen,
    "wall_time_seconds": int(wall), "model": model,
    "repo_commit": commit or None, "tool_version": ver or None,
    "harness": "opencode", "provider": (model.split("/", 1)[0] if "/" in model else "opencode"),
    "auth_mode": "opencode_cli", "mode": "single_prompt",
    "opencode_exit_code": rc, "attempts": int(attempts), "output_tokens": otok,
    "answer_chars": achars,
    "cost_usd_note": "opencode subscription bills off-platform; per-token cost left null",
}
kind = KIND.get(rc, "opencode_session_failed")
if kind:
    meta["watchdog_kind"] = kind
# Failure classes that are NOT real 0.0 data and must be flagged so they are not
# trusted as genuine ties: (1) a provider cap error (the answer IS an
# `llm_call_failed`/"Operation not allowed" blob = ollama-cloud rate-limit/session
# cap; re-run the repo after reset), (2) a true no-output hang (rc!=0 AND 0
# tokens), and (3) an empty/truncated final answer (tokens streamed but the
# answer text is below min_chars -- the 0-char degenerate runs). A capped/stalled
# run that DID leave a long real answer stays a valid (truncated) datum.
# Check provider-error FIRST: its blob is ~94 chars so it also trips the
# min_chars gate, but the cap diagnosis is the more actionable one.
if perr == 1:
    meta["error"] = "provider_cap_error"
    meta["watchdog_kind"] = "provider_cap_error"
    meta["note"] = "final answer was an ollama-cloud provider error (llm_call_failed / 'Operation not allowed'); likely a rate-limit/session cap -- re-run this repo after the cap resets, do NOT score as a 0.0"
elif offload == 1:
    meta["error"] = "answer_offloaded_to_file"
    meta["note"] = "model wrote its audit to a scratch/repo .json file and returned a pointer stub (retried, still offloaded); scores 0.0 but is NOT a real answer -- exclude per the validity gate, do NOT trust as a tie"
elif achars < min_chars:
    meta["error"] = "empty_final_answer"
    meta["note"] = f"final answer only {achars} chars (< {min_chars}); truncated/empty stream, retried and still short -- not a real 0.0"
elif rc != 0 and otok == 0:
    meta["error"] = "opencode_session_failed"
elif rc != 0:
    meta["note"] = f"watchdog stopped ({kind}) but produced {otok} output tokens and a {achars}-char answer; kept as a truncated-but-valid run"
print(json.dumps(meta, indent=2))
PY
  if [ "${perr:-0}" -eq 1 ]; then flag=" *** INVALID: provider cap error (Operation not allowed) -- re-run after reset ***"; hclass=cap;
  elif [ "${offload:-0}" -eq 1 ]; then flag=" *** INVALID: answer offloaded to file (pointer stub) ***"; hclass=offload;
  elif [ "${achars:-0}" -lt "$OPENCODE_MIN_ANSWER_CHARS" ]; then flag=" *** INVALID: empty/truncated answer ***"; hclass=truncated;
  elif [ "$rc" -eq 126 ]; then flag=""; hclass=hang;
  elif [ "$rc" -ne 0 ]; then flag=""; hclass=watchdog;
  else flag=""; hclass=ok; fi
  echo "[opencode]   $tool rc=$rc wall=${wall}s attempts=$attempts out_tok=$otok answer_chars=$achars$flag" >&2
  # Throttle-health line per session so onset is observable live.
  pace_health_log "$REPO" "$tool" "$wall" "$otok" "$achars" "$attempts" "$hclass"
done

SJ=(--tool "$TOOLS_CSV" --repo "$REPO")
bash "$BENCH_DIR/score.sh"  "${SJ[@]}"
bash "$BENCH_DIR/judge.sh"  "${SJ[@]}" --via-cli
bash "$BENCH_DIR/report.sh" --md
echo "[opencode] done, see bench/results/{${TOOLS_CSV}}/$REPO/ (channels.json per arm)" >&2
