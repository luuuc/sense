#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
BENCH_PROJECT_ROOT="$PROJECT_ROOT"
TOOLS_DIR="$BENCH_DIR/tools"
SCENARIOS_DIR="$BENCH_DIR/scenarios"
RESULTS_DIR="$BENCH_DIR/results"
LIB_DIR="$BENCH_DIR/lib"
# shellcheck disable=SC1091
source "$LIB_DIR/load-env.sh"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"
REF_DIR="$SENSE_BENCH_ROOT/_reference"
READY_POLL_INTERVAL=5
READY_POLL_MAX=720  # 60 minutes at 5s intervals
SETUP_TIMEOUT=600   # 10 minutes max for initial setup
# Card 15 history: tried $2 → too generous, $0.75 → zeroed 5/12 sessions.
# Per-session budget derived per repo from BUDGET_PER_REPO[repo] in
# lib/scorer.py (sized at ~2× observed healthy session cost). flask/gin
# get $1.00, axum/javalin $1.75, discourse $2.00, nextjs $2.25; unknown
# repos fall back to DEFAULT_BUDGET_USD ($1.50). The single global
# MAX_BUDGET_USD below stays as a CLI override (--budget) but is not used
# in the default path. Partial transcripts (over-budget but with answer
# content) are scored normally — scorer.py marks them `constrained: True`
# and fairness comparison still works. See bench/end-goal.md.
MAX_BUDGET_USD=""
# Per-session wall-time timeout = 1.0 × TIME_CEILINGS[repo] (was 2×).
# At 1× ceiling, time_efficiency is already 0, so killing at that point
# costs nothing in score terms and bounds the worst case tightly. No floor —
# the ceilings are calibrated per repo. --timeout overrides.
SESSION_TIMEOUT=""
SESSION_TIMEOUT_FLOOR=300   # absolute floor: don't go below 5 min even for tiny repos
SESSION_TIMEOUT_DEFAULT=480  # fallback for unknown repos (= DEFAULT_TIME_CEILING × 1)

# --- Argument parsing ---

FILTER_TOOLS=""
FILTER_REPOS=""
DRY_RUN=false
RESET=false
NUM_RUNS=1
MODEL=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool)  FILTER_TOOLS="$2"; shift 2 ;;
    --repo)  FILTER_REPOS="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    --reset) RESET=true; shift ;;
    --budget) MAX_BUDGET_USD="$2"; shift 2 ;;
    --timeout) SESSION_TIMEOUT="$2"; shift 2 ;;
    --runs) NUM_RUNS="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: run.sh [--tool t1,t2] [--repo r1,r2] [--runs N] [--model MODEL] [--dry-run] [--reset] [--budget USD] [--timeout SECS]"
      echo ""
      echo "Runs scenario-based evaluation: tool x scenario (repo)."
      echo ""
      echo "Options:"
      echo "  --tool    Comma-separated tool filter (e.g. sense,baseline)"
      echo "  --repo    Comma-separated repo filter (e.g. flask,discourse)"
      echo "  --runs    Number of runs per scenario for variance estimation (default: 1)"
      echo "  --model   Claude model to use (e.g. sonnet, opus)"
      echo "  --dry-run Show what would run without executing Claude sessions"
      echo "  --reset   Delete existing indexes and workspaces"
      echo "  --budget  Max USD per Claude session (default: BUDGET_PER_REPO[repo] from lib/scorer.py)"
      echo "  --timeout Max seconds per Claude session (default: 1× TIME_CEILINGS[repo], floor ${SESSION_TIMEOUT_FLOOR}s)"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

# --- Helpers ---

matches_filter() {
  local value="$1"
  local filter="$2"
  [[ -z "$filter" ]] && return 0
  echo "$filter" | tr ',' '\n' | grep -qx "$value"
}

tool_repo_path() {
  local tool="$1" repo="$2"
  echo "$SENSE_BENCH_ROOT/$tool/$repo"
}

# Derive per-repo session timeout = 2 × TIME_CEILINGS[repo].
# TIME_CEILINGS lives in lib/scorer.py — the canonical source the loop
# will tune in ±20% steps. Bash 3.2 (macOS default) has no associative
# arrays; the Python invocation is ~50ms vs. multi-minute claude sessions
# so caching isn't worth the portability cost.
compute_session_timeout() {
  local repo="$1"
  if [[ -n "$SESSION_TIMEOUT" ]]; then
    echo "$SESSION_TIMEOUT"
    return 0
  fi
  local secs
  secs=$(python3 -c "
import sys
sys.path.insert(0, '$LIB_DIR')
from scorer import TIME_CEILINGS, DEFAULT_TIME_CEILING
ceil = TIME_CEILINGS.get('$repo', DEFAULT_TIME_CEILING)
out = max($SESSION_TIMEOUT_FLOOR, ceil)
print(out)
" 2>/dev/null) || secs="$SESSION_TIMEOUT_DEFAULT"
  echo "$secs"
}

# Per-session budget cap. Defaults to BUDGET_PER_REPO[repo] from
# lib/scorer.py; --budget overrides globally.
compute_session_budget() {
  local repo="$1"
  if [[ -n "$MAX_BUDGET_USD" ]]; then
    echo "$MAX_BUDGET_USD"
    return 0
  fi
  local bud
  bud=$(python3 -c "
import sys
sys.path.insert(0, '$LIB_DIR')
from scorer import BUDGET_PER_REPO, DEFAULT_BUDGET_USD
print(BUDGET_PER_REPO.get('$repo', DEFAULT_BUDGET_USD))
" 2>/dev/null) || bud="1.50"
  echo "$bud"
}

ensure_tool_repo() {
  local tool="$1" repo="$2"
  local dest
  dest=$(tool_repo_path "$tool" "$repo")
  if [[ -d "$dest/.git" ]]; then
    return 0
  fi
  local ref="$REF_DIR/$repo"
  if [[ ! -d "$ref/.git" ]]; then
    log "  ERROR: reference repo missing: $ref"
    log "  Run: cd ../bench && bash bootstrap-repos.sh --repo $repo"
    return 1
  fi
  log "  cloning $repo for $tool (--reference from _reference/)..."
  mkdir -p "$(dirname "$dest")"
  git clone --quiet --reference "$ref" "$ref" "$dest"
  local pinned
  pinned=$(python3 -c "
import sys,json
v=json.load(open(sys.argv[1])).get(sys.argv[2])
if isinstance(v, dict): v=v.get('commit')
print(v if v else '')
" "../bench/PINNED_COMMITS.json" "$repo")
  if [[ -n "$pinned" ]]; then
    (cd "$dest" && git -c advice.detachedHead=false checkout "$pinned" --quiet)
  fi
}

PINNED_COMMITS="$PROJECT_ROOT/bench/PINNED_COMMITS.json"

TIMEOUT_CMD=""
if command -v timeout &>/dev/null; then
  TIMEOUT_CMD="timeout"
elif command -v gtimeout &>/dev/null; then
  TIMEOUT_CMD="gtimeout"
fi

run_with_timeout() {
  local secs="$1"; shift
  if [[ -n "$TIMEOUT_CMD" ]]; then
    "$TIMEOUT_CMD" "$secs" "$@"
  else
    "$@" &
    local pid=$!
    ( sleep "$secs" && kill "$pid" 2>/dev/null ) &
    local watchdog=$!
    wait "$pid" 2>/dev/null
    local rc=$?
    kill "$watchdog" 2>/dev/null
    wait "$watchdog" 2>/dev/null
    return $rc
  fi
}

timestamp() {
  date +%Y-%m-%dT%H:%M:%S
}

log() {
  echo "[$(timestamp)] $*" >&2
}

# --- Discover tools ---

tools=()
if [[ -n "$FILTER_TOOLS" ]]; then
  while IFS= read -r name; do
    [[ -f "$TOOLS_DIR/$name.sh" ]] && tools+=("$name")
  done < <(echo "$FILTER_TOOLS" | tr ',' '\n')
else
  for script in "$TOOLS_DIR"/*.sh; do
    [[ -f "$script" ]] || continue
    name="$(basename "$script" .sh)"
    tools+=("$name")
  done
fi

# --- Discover scenarios ---

scenarios=()
scenario_files=()
for scenariofile in "$SCENARIOS_DIR"/*.yaml; do
  [[ -f "$scenariofile" ]] || continue
  name="$(basename "$scenariofile" .yaml)"
  repo=$(python3 -c "
import sys,yaml
d=yaml.safe_load(open(sys.argv[1]))
print(d['repo'])
" "$scenariofile" 2>/dev/null || echo "")
  if [[ -z "$repo" ]]; then continue; fi
  matches_filter "$repo" "$FILTER_REPOS" || continue
  scenarios+=("$name")
  scenario_files+=("$scenariofile")
done

scenario_repo() {
  local name="$1"
  for i in "${!scenarios[@]}"; do
    [[ "${scenarios[$i]}" == "$name" ]] && echo "$(python3 -c "
import yaml
print(yaml.safe_load(open('${scenario_files[$i]}'))['repo'])
")" && return
  done
  echo ""
}

if [[ ${#tools[@]} -eq 0 ]]; then
  echo "No tools found in bench/tools/" >&2
  exit 1
fi
if [[ ${#scenarios[@]} -eq 0 ]]; then
  echo "No scenarios matched" >&2
  exit 1
fi

total_runs=$((${#tools[@]} * ${#scenarios[@]} * NUM_RUNS))
log "Evaluation: ${#tools[@]} tools x ${#scenarios[@]} scenarios = $total_runs runs"
log "Tools: ${tools[*]}"
log "Scenarios: ${scenarios[*]}"

if $DRY_RUN; then
  echo ""
  echo "=== DRY RUN ==="
  echo ""
  run_num=0
  for tool in "${tools[@]}"; do
    for scenario_name in "${scenarios[@]}"; do
      repo=$(scenario_repo "$scenario_name")
      for run_idx in $(seq 1 $NUM_RUNS); do
        run_num=$((run_num + 1))
        if [[ $NUM_RUNS -gt 1 ]]; then
          echo "  [$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name run=$run_idx/$NUM_RUNS"
        else
          echo "  [$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name"
        fi
      done
    done
  done
  echo ""
  echo "Estimated cost: ~\$$(echo "$total_runs * 0.10" | bc) (at ~\$0.10/session)"
  exit 0
fi

# --- Suppress Sense hook injection ---
export SENSE_BENCH=1

# --- Neutralize user-level config for fair benchmarking ---
USER_SETTINGS="$HOME/.claude/settings.json"
USER_MCP_CONFIG="$HOME/.claude/.mcp.json"
USER_SETTINGS_BACKUP=""
USER_MCP_BACKUP=""

restore_user_settings() {
  if [[ -n "${USER_SETTINGS_BACKUP:-}" && -f "$USER_SETTINGS_BACKUP" ]]; then
    cp "$USER_SETTINGS_BACKUP" "$USER_SETTINGS"
    rm -f "$USER_SETTINGS_BACKUP"
    log "Restored ~/.claude/settings.json"
  fi
  if [[ -n "${USER_MCP_BACKUP:-}" && -f "$USER_MCP_BACKUP" ]]; then
    cp "$USER_MCP_BACKUP" "$USER_MCP_CONFIG"
    rm -f "$USER_MCP_BACKUP"
    log "Restored ~/.claude/.mcp.json"
  fi
}

strip_user_hooks() {
  python3 -c "
import json
with open('$USER_SETTINGS') as f:
    d = json.load(f)
d.pop('hooks', None)
with open('$USER_SETTINGS', 'w') as f:
    json.dump(d, f, indent=2)
    f.write('\n')
"
}

strip_user_mcp() {
  python3 -c "
import json
with open('$USER_MCP_CONFIG') as f:
    d = json.load(f)
d['mcpServers'] = {}
with open('$USER_MCP_CONFIG', 'w') as f:
    json.dump(d, f, indent=2)
    f.write('\n')
"
}

cleanup() {
  restore_user_settings
}
trap cleanup EXIT

if [[ -f "$USER_SETTINGS" ]] && grep -q 'cbm-' "$USER_SETTINGS" 2>/dev/null; then
  USER_SETTINGS_BACKUP=$(mktemp)
  cp "$USER_SETTINGS" "$USER_SETTINGS_BACKUP"
  strip_user_hooks
  log "Stripped hooks from ~/.claude/settings.json"
fi

if [[ -f "$USER_MCP_CONFIG" ]] && grep -q 'mcpServers' "$USER_MCP_CONFIG" 2>/dev/null; then
  USER_MCP_BACKUP=$(mktemp)
  cp "$USER_MCP_CONFIG" "$USER_MCP_BACKUP"
  strip_user_mcp
  log "Stripped MCP servers from ~/.claude/.mcp.json"
fi

# --- Main loop ---

run_num=0
passed=0
failed=0
skipped=0

for tool in "${tools[@]}"; do
  # Restore user-level hooks + MCP for codebase-memory-mcp (part of its offering)
  if [[ "$tool" == "codebase-memory-mcp" ]]; then
    [[ -n "${USER_SETTINGS_BACKUP:-}" ]] && cp "$USER_SETTINGS_BACKUP" "$USER_SETTINGS"
    [[ -n "${USER_MCP_BACKUP:-}" ]] && cp "$USER_MCP_BACKUP" "$USER_MCP_CONFIG"
  else
    [[ -n "${USER_SETTINGS_BACKUP:-}" ]] && strip_user_hooks
    [[ -n "${USER_MCP_BACKUP:-}" ]] && strip_user_mcp
  fi

  for scenario_name in "${scenarios[@]}"; do
      repo=$(scenario_repo "$scenario_name")
    scenario_file="$SCENARIOS_DIR/$scenario_name.yaml"

    if ! ensure_tool_repo "$tool" "$repo"; then
      for run_idx in $(seq 1 $NUM_RUNS); do
        run_num=$((run_num + 1))
        log "[$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name"
        log "  SKIP: reference repo not available"
        skipped=$((skipped + 1))
      done
      continue
    fi

    rp=$(tool_repo_path "$tool" "$repo")
    workspace="$SENSE_BENCH_ROOT/$tool/$repo/.workspace"
    mkdir -p "$workspace"

    if $RESET; then
      log "  resetting $tool for $repo..."
      case "$tool" in
        codebase-memory-mcp) rm -rf "$workspace/.cbm-cache" ;;
        gitnexus)  rm -rf "$rp/.gitnexus" ;;
        grepai)    rm -rf "$rp/.grepai" ;;
        probe)     ;; # stateless, nothing to reset
        roam)      rm -rf "$rp/.roam" ;;
        sense)     rm -rf "$rp/.sense" ;;
        serena)    rm -rf "$rp/.serena" ;;
        tokensave) rm -rf "$rp/.tokensave" ;;
      esac
      rm -rf "$workspace"
      mkdir -p "$workspace"
    fi

    # Setup if not already indexed
    "$TOOLS_DIR/$tool.sh" --check-ready "$rp" "$workspace" >/dev/null 2>/dev/null && ready_rc=0 || ready_rc=$?
    if [[ $ready_rc -eq 0 ]]; then
      log "  $tool x $repo already indexed — writing config only"
      "$TOOLS_DIR/$tool.sh" --write-config "$rp" "$workspace"
    else
      log "  setting up $tool for $repo..."
      if ! run_with_timeout "$SETUP_TIMEOUT" "$TOOLS_DIR/$tool.sh" "$rp" "$workspace"; then
        log "  FAIL: setup failed"
        run_num=$((run_num + NUM_RUNS))
        failed=$((failed + NUM_RUNS))
        continue
      fi

      log "  waiting for index readiness..."
      ready=false
      broken=false
      for i in $(seq 1 $READY_POLL_MAX); do
        "$TOOLS_DIR/$tool.sh" --check-ready "$rp" "$workspace" >/dev/null 2>/dev/null && rc=0 || rc=$?
        if [[ $rc -eq 0 ]]; then
          ready=true; break
        elif [[ $rc -eq 2 ]]; then
          broken=true; break
        fi
        sleep $READY_POLL_INTERVAL
      done

      if [[ "$ready" != "true" ]]; then
        run_num=$((run_num + NUM_RUNS))
        skipped=$((skipped + NUM_RUNS))
        continue
      fi
    fi

    # Build the full scenario prompt
    prompt=$(python3 "$LIB_DIR/scenario.py" "$scenario_file" --prompt)
    claude_md=$(cat "$workspace/CLAUDE.md" 2>/dev/null || echo "")

    for run_idx in $(seq 1 $NUM_RUNS); do
      run_num=$((run_num + 1))
      if [[ $NUM_RUNS -gt 1 ]]; then
        log "[$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name run=$run_idx/$NUM_RUNS"
        result_dir="$RESULTS_DIR/$tool/$repo/run-$run_idx"
      else
        log "[$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name"
        result_dir="$RESULTS_DIR/$tool/$repo"
      fi
      mkdir -p "$result_dir"

      session_budget=$(compute_session_budget "$repo")
      claude_args=(
        -p "$prompt"
        --verbose
        --append-system-prompt "$claude_md"
        --output-format stream-json
        --permission-mode bypassPermissions
        --disallowed-tools "Agent"
        --max-budget-usd "$session_budget"
      )

      if [[ -n "$MODEL" ]]; then
        claude_args+=(--model "$MODEL")
      fi

      if [[ -f "$workspace/.mcp.json" ]]; then
        claude_args+=(--mcp-config "$workspace/.mcp.json")
      fi

      session_timeout=$(compute_session_timeout "$repo")
      log "  running Claude session (budget=\$${session_budget} timeout=${session_timeout}s)..."
      start_time=$(date +%s)

      (cd "$rp" && run_with_timeout "$session_timeout" claude "${claude_args[@]}" > "$result_dir/transcript.json" 2>"$result_dir/claude.log") && claude_rc=0 || claude_rc=$?

      # Credit-exhausted retry: the claude CLI can fall back to OAuth
      # subscription when ANTHROPIC_API_KEY is unset. urllib direct-API
      # callers can't — see lib/judge.py — but the CLI's auth flow is
      # special. Try once more with the key unset; if subscription is
      # active, the second attempt will succeed.
      if [[ $claude_rc -ne 0 && -n "${ANTHROPIC_API_KEY:-}" ]]; then
        if grep -qi 'credit balance is too low\|invalid_api_key' "$result_dir/claude.log" 2>/dev/null; then
          log "  CREDIT EXHAUSTED on API key — retrying once on subscription..."
          (cd "$rp" && ANTHROPIC_API_KEY="" run_with_timeout "$session_timeout" claude "${claude_args[@]}" > "$result_dir/transcript.json" 2>>"$result_dir/claude.log") && claude_rc=0 || claude_rc=$?
        fi
      fi
      end_time=$(date +%s)
      wall_time=$((end_time - start_time))

      if [[ $claude_rc -eq 0 ]]; then
        log "  done in ${wall_time}s"

        tool_version=$(grep -m1 '^TOOL_VERSION=' "$TOOLS_DIR/$tool.sh" 2>/dev/null | cut -d'"' -f2 || echo "")
        repo_commit=$(cd "$rp" && git rev-parse --short HEAD 2>/dev/null || echo "")

        python3 -c "
import json, sys
meta = {
    'tool': sys.argv[1], 'repo': sys.argv[2], 'scenario': sys.argv[3],
    'wall_time_seconds': int(sys.argv[4]), 'max_budget_usd': float(sys.argv[5]),
    'timestamp': sys.argv[6], 'tool_version': sys.argv[7] or None,
    'repo_commit': sys.argv[8] or None,
    'model': sys.argv[9] or None,
}
json.dump(meta, sys.stdout, indent=2)
print()
" "$tool" "$repo" "$scenario_name" "$wall_time" "$session_budget" "$(timestamp)" "$tool_version" "$repo_commit" "$MODEL" > "$result_dir/run_meta.json"

        passed=$((passed + 1))
      else
        log "  FAIL: Claude session failed after ${wall_time}s"
        python3 -c "
import json, sys
meta = {
    'tool': sys.argv[1], 'repo': sys.argv[2], 'scenario': sys.argv[3],
    'wall_time_seconds': int(sys.argv[4]), 'max_budget_usd': float(sys.argv[5]),
    'timestamp': sys.argv[6], 'error': 'claude_session_failed',
    'claude_exit_code': int(sys.argv[7]),
    'model': sys.argv[8] or None,
}
json.dump(meta, sys.stdout, indent=2)
print()
" "$tool" "$repo" "$scenario_name" "$wall_time" "$session_budget" "$(timestamp)" "$claude_rc" "$MODEL" > "$result_dir/run_meta.json"
        failed=$((failed + 1))
      fi
    done
  done
done

log ""
log "=== Evaluation complete ==="
log "  Total: $total_runs | Passed: $passed | Failed: $failed | Skipped: $skipped"
log "  Results in: $RESULTS_DIR/"
