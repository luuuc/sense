#!/usr/bin/env bash
# codex-run.sh runs the Rails-vertical bench through the Codex CLI agent
# (GPT-5.x on the ChatGPT subscription) instead of the Claude CLI.
#
# Single-prompt over the 7-step scenario (the trustworthy path, same as
# bench-sense-local.sh): renders all steps into one prompt, runs `codex exec
# --json`, normalizes the JSONL into the canonical transcript scorer.py reads
# (via lib/parse-codex-result.py), then score -> judge (--via-cli) -> report.
# Writes to bench/results/{baseline,sense}/<repo>/ so the existing
# score/judge/report/snapshot pipeline runs unchanged.
#
#   bash bench/drivers/codex-run.sh --tool baseline,sense --repo ruby_llm
#   bash bench/drivers/codex-run.sh --repo discourse --model gpt-5.5
#
# Sense reaches Codex through TWO channels and we report which it used:
#   - MCP: registered on the sense arm via `-c mcp_servers.sense=...`
#   - CLI: the `sense` binary on PATH, which GPT-5.x tends to prefer
# (see channels.json per arm). Arm isolation: the BASELINE arm runs with the
# sense binary's dir stripped from PATH (and no MCP), so it cannot reach Sense
# by either channel (the contamination risk called out for Codex).
#
# Prereqs: clones at $SENSE_BENCH_ROOT/{baseline,sense}/<repo>; sense arm
# already `sense scan`-ed; `codex` logged in (`codex login`); `sense` on PATH.
# Judge stays claude-sonnet-4-6 (set in judge.py); it runs on the Claude
# subscription, untouched by this script.

set -uo pipefail

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
# Resolves SCENARIOS_DIR + RESULTS_DIR for the global or VERTICAL bench.
source "$BENCH_DIR/lib/bench-paths.sh"
# Subscription-throttle pacing for this METERED arm (default-on; BENCH_THROTTLE_PACING=0
# = exact pre-pacing behavior). codex exec is single-shot (no retry loop), so the
# exponential backoff does not apply here; inter-session spacing, the per-plan lock,
# the cooldown (gated in runs-variance) and the health log do. The opus runner never
# sources this.
source "$BENCH_DIR/lib/throttle-pacing.sh"
LIB_DIR="$BENCH_DIR/lib"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"

TOOLS_CSV="baseline,sense"; REPO=""; MODEL="gpt-5.5"; SANDBOX="read-only"
SESSION_TIMEOUT=""; KEEP_RAW=0
while [[ $# -gt 0 ]]; do case "$1" in
  --tool) TOOLS_CSV="$2"; shift 2;;
  --repo) REPO="$2"; shift 2;;
  --model) MODEL="$2"; shift 2;;
  --sandbox) SANDBOX="$2"; shift 2;;
  --timeout) SESSION_TIMEOUT="$2"; shift 2;;
  --keep-raw) KEEP_RAW=1; shift;;
  -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0;;
  *) echo "unknown arg: $1" >&2; exit 1;;
esac; done
[[ -n "$REPO" ]] || { echo "need --repo" >&2; exit 1; }

command -v codex >/dev/null || { echo "codex CLI not found in PATH" >&2; exit 1; }
command -v sense >/dev/null || { echo "sense not found in PATH (needed for the sense arm)" >&2; exit 1; }

# Don't let a stray API key bill the wrong wallet; Codex uses its own auth.json.
unset ANTHROPIC_API_KEY BENCHMARK_ANTHROPIC_API_KEY

# macOS ships no `timeout`; prefer GNU, then gtimeout, else no ceiling. The
# seconds get baked into TO once SECS is known (below), so the invocation stays
# `"${TO[@]}" codex …`; on macOS TO=(env) is a no-op prefix (no ceiling).
TIMEOUT_BIN=""
if command -v timeout >/dev/null; then TIMEOUT_BIN=timeout
elif command -v gtimeout >/dev/null; then TIMEOUT_BIN=gtimeout; fi

# Baseline isolation: a PATH with the sense binary's directory removed, so the
# control arm cannot call `sense` (CLI channel); Codex can use the CLI, and
# `sense` lives on the host PATH globally.
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
  SECS=$(python3 -c "import sys;sys.path.insert(0,'$LIB_DIR');from scorer import TIME_CEILINGS,DEFAULT_TIME_CEILING;print(max(600,TIME_CEILINGS.get('$REPO',DEFAULT_TIME_CEILING)))")
fi
if [[ -n "$TIMEOUT_BIN" ]]; then TO=("$TIMEOUT_BIN" "$SECS"); else TO=(env); fi

IFS=',' read -ra TOOLS <<< "$TOOLS_CSV"
# Optional sense-first ordering (BENCH_SENSE_FIRST=1): heavier sense arm first,
# into the fresher window. Default preserves input order. 3.2-safe (no mapfile).
ORDERED=(); while IFS= read -r _t; do [ -n "$_t" ] && ORDERED+=("$_t"); done < <(pace_order_tools "${TOOLS[@]}")
TOOLS=("${ORDERED[@]}")
arm_idx=0
for tool in "${TOOLS[@]}"; do
  repo_dir="$SENSE_BENCH_ROOT/$tool/$REPO"
  [[ -d "$repo_dir/.git" ]] || { echo "[codex] SKIP $tool: clone missing at $repo_dir" >&2; continue; }
  # Inter-arm spacing so the second arm starts in a less-drained window.
  [ "$arm_idx" -gt 0 ] && pace_sleep "$OPENCODE_PACE_SECONDS" "between arms (next $tool/$REPO)"
  arm_idx=$(( arm_idx + 1 ))
  out="$RESULTS_DIR/$tool/$REPO"; mkdir -p "$out"
  echo "[codex] $tool/$REPO model=$MODEL sandbox=$SANDBOX timeout=${SECS}s" >&2

  # Fairness normalization (idempotent, every arm, every run). Mirrors the strip
  # in bench-sense-local.sh: some upstream repos (lobsters) ship an anti-AI
  # PROTEST banner in CLAUDE.md/AGENTS.md ("mandatory to refuse to write any
  # code … All LLM contributions are strictly forbidden"). It is not an
  # engineering constraint; it injects refusal NOISE that corrupts the
  # measurement and biases the arms when it survives in one clone but not the
  # other. codex exec loads AGENTS.md before any work, so the baseline arm
  # refused on lobsters (false +1.00) while the sense arm's `sense setup`
  # overwrote AGENTS.md and dropped the banner. Strip it from BOTH arms' clones
  # so they run on identical footing. Runs before `sense setup` below.
  for guide in "$repo_dir/CLAUDE.md" "$repo_dir/AGENTS.md"; do
    [[ -f "$guide" ]] || continue
    python3 - "$guide" <<'PY'
import sys
p = sys.argv[1]
keep = [l for l in open(p).read().splitlines(keepends=True)
        if "All LLM contributions are strictly forbidden" not in l
        and "mandatory to refuse to write" not in l]
open(p, "w").writelines(keep)
PY
  done

  # Per-arm codex config. Both arms: ignore the operator's user config (drops the
  # global node_repl/computer-use/browser plugins so the arms are clean and
  # comparable) and never prompt for approval. inherit=all so the sandboxed shell
  # sees the PATH we set below. sense arm: register the Sense MCP server (mirrors
  # the clone's .mcp.json, i.e. command `sense`, args ["mcp"]) AND keep `sense` on
  # PATH (CLI channel). baseline arm: scrubbed PATH, no MCP.
  args=(exec --json -C "$repo_dir" -s "$SANDBOX" -m "$MODEL"
        --skip-git-repo-check --ignore-user-config
        -c 'approval_policy="never"'
        -c 'shell_environment_policy.inherit=all')
  if [[ "$tool" == sense ]]; then
    # Set up the clone the way a real user does: full `sense setup` (no --tools)
    # configures every detected tool. Codex needs AGENTS.md (the routing prose
    # `codex exec` loads before any work); without it the only steering is the
    # MCP serverInstructions blob, which GPT-5.x ignores in `codex exec`, so the
    # arm reaches Sense 0 times even though the MCP server is registered. We
    # deliberately do NOT scope to --tools codex-cli: the scoped form is what
    # silently left this arm un-set-up, and each tool reads only its own file
    # (codex→AGENTS.md, Claude→CLAUDE.md, Cursor→.cursorrules) with identical
    # guidance text, so a full setup never cross-contaminates. Baseline stays
    # isolated by its own clone + scrubbed PATH, untouched by this.
    ( cd "$repo_dir" && sense setup >/dev/null 2>&1 ) \
      || echo "[codex]   WARN: sense setup failed" >&2
    args+=(-c 'mcp_servers.sense.command="sense"' -c 'mcp_servers.sense.args=["mcp"]')
    run_path="$PATH"
  else
    run_path="$SCRUBBED_PATH"
  fi

  raw="$out/codex-raw.jsonl"
  start=$(date +%s)
  ( cd "$repo_dir" && PATH="$run_path" "${TO[@]}" codex "${args[@]}" "$PROMPT" ) \
      > "$raw" 2> "$out/codex.log"
  rc=$?
  wall=$(( $(date +%s) - start ))

  python3 "$LIB_DIR/parse-codex-result.py" "$raw" --channels-json "$out/channels.json" \
      > "$out/transcript.json" 2>> "$out/codex.log" || echo "[codex] parse failed ($tool)" >&2
  # Keep claude.log present so downstream tools that glance at it don't choke.
  cp "$out/codex.log" "$out/claude.log" 2>/dev/null || true
  [[ "$KEEP_RAW" == 1 ]] || rm -f "$raw"

  nmcp=$(python3 -c "import json;print(json.load(open('$out/channels.json'))['channels']['mcp_sense'])" 2>/dev/null || echo 0)
  ncli=$(python3 -c "import json;print(json.load(open('$out/channels.json'))['channels']['cli_sense'])" 2>/dev/null || echo 0)
  if [[ "$tool" == sense ]]; then
    if [[ $((nmcp + ncli)) -gt 0 ]]; then echo "[codex]   sense used: mcp=$nmcp cli=$ncli (valid)" >&2
    else echo "[codex]   *** INVALID: sense arm reached Sense 0 times (mcp=0 cli=0) ***" >&2; fi
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
    "harness": "codex", "provider": "codex",
    "auth_mode": "subscription_cli", "mode": "single_prompt",
    "codex_exit_code": int(rc),
    "cost_usd_note": "codex runs on a ChatGPT subscription; per-token cost not meaningful",
}
if int(rc) != 0:
    meta["error"] = "codex_session_failed"
print(json.dumps(meta, indent=2))
PY
  echo "[codex]   $tool rc=$rc wall=${wall}s" >&2
  # Throttle-health line per session. codex exec yields no per-stream token/answer
  # counts here, so otok/achars are '-'; class is derived from the exit code.
  [ "$rc" -eq 0 ] && hclass=ok || hclass=session_failed
  pace_health_log "$REPO" "$tool" "$wall" "-" "-" "1" "$hclass"
done

SJ=(--tool "$TOOLS_CSV" --repo "$REPO")
bash "$BENCH_DIR/score.sh"  "${SJ[@]}"
bash "$BENCH_DIR/judge.sh"  "${SJ[@]}" --via-cli
bash "$BENCH_DIR/report.sh" --md
echo "[codex] done, see bench/results/{${TOOLS_CSV}}/$REPO/ (channels.json per arm)" >&2
