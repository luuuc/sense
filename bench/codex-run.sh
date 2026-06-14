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
#   bash bench/codex-run.sh --tool baseline,sense --repo ruby_llm
#   bash bench/codex-run.sh --repo discourse --model gpt-5.4
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
# Judge stays claude-opus-4-7 (set in judge.py); it runs on the Claude
# subscription, untouched by this script.

set -uo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
SCENARIOS_DIR="$BENCH_DIR/scenarios"
RESULTS_DIR="$BENCH_DIR/results"
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
for tool in "${TOOLS[@]}"; do
  repo_dir="$SENSE_BENCH_ROOT/$tool/$REPO"
  [[ -d "$repo_dir/.git" ]] || { echo "[codex] SKIP $tool: clone missing at $repo_dir" >&2; continue; }
  out="$RESULTS_DIR/$tool/$REPO"; mkdir -p "$out"
  echo "[codex] $tool/$REPO model=$MODEL sandbox=$SANDBOX timeout=${SECS}s" >&2

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
done

SJ=(--tool "$TOOLS_CSV" --repo "$REPO")
bash "$BENCH_DIR/score.sh"  "${SJ[@]}"
bash "$BENCH_DIR/judge.sh"  "${SJ[@]}" --via-cli
bash "$BENCH_DIR/report.sh" --md
echo "[codex] done, see bench/results/{${TOOLS_CSV}}/$REPO/ (channels.json per arm)" >&2
