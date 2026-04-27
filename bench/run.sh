#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
TOOLS_DIR="$BENCH_DIR/tools"
TASKS_DIR="$BENCH_DIR/tasks"
RESULTS_DIR="$BENCH_DIR/results"
LIB_DIR="$BENCH_DIR/lib"
REPOS_DIR="$BENCH_DIR/repos"
SENSE_REPO="$(cd "$BENCH_DIR/.." && pwd)"

READY_POLL_INTERVAL=5
READY_POLL_MAX=720  # 60 minutes at 5s intervals
MAX_BUDGET_USD="1.00"
SESSION_TIMEOUT=600  # 10 minutes per Claude session

# --- Argument parsing ---

FILTER_TOOLS=""
FILTER_REPOS=""
FILTER_TASKS=""
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool)  FILTER_TOOLS="$2"; shift 2 ;;
    --repo)  FILTER_REPOS="$2"; shift 2 ;;
    --task)  FILTER_TASKS="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    --budget) MAX_BUDGET_USD="$2"; shift 2 ;;
    --timeout) SESSION_TIMEOUT="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: run.sh [--tool t1,t2] [--repo r1,r2] [--task t1,t2] [--dry-run] [--budget USD] [--timeout SECS]"
      echo ""
      echo "Runs the competitive evaluation harness: tool × repo × task."
      echo ""
      echo "Options:"
      echo "  --tool    Comma-separated tool filter (e.g. sense,baseline)"
      echo "  --repo    Comma-separated repo filter (e.g. sense,discourse)"
      echo "  --task    Comma-separated task filter (e.g. callers,blast-radius)"
      echo "  --dry-run Show what would run without executing Claude sessions"
      echo "  --budget  Max USD per Claude session (default: $MAX_BUDGET_USD)"
      echo "  --timeout Max seconds per Claude session (default: $SESSION_TIMEOUT)"
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

repo_path() {
  local repo="$1"
  if [[ "$repo" == "sense" ]]; then
    echo "$SENSE_REPO"
  else
    echo "$REPOS_DIR/$repo"
  fi
}

PINNED_COMMITS="$REPOS_DIR/PINNED_COMMITS.json"

check_pinned_commit() {
  local repo="$1"
  local rp="$2"
  if [[ ! -f "$PINNED_COMMITS" ]]; then
    return 0
  fi
  local pinned
  pinned=$(python3 -c "import sys,json; v=json.load(open(sys.argv[1])).get(sys.argv[2]); print(v if v else '')" "$PINNED_COMMITS" "$repo")
  if [[ -z "$pinned" ]]; then
    log "  WARNING: no pinned commit for $repo — ground-truth may not match"
    return 0
  fi
  local actual
  actual=$(cd "$rp" && git rev-parse HEAD 2>/dev/null || echo "")
  if [[ "$actual" != "$pinned" ]]; then
    log "  WARNING: $repo is at ${actual:0:12} but pinned to ${pinned:0:12} — ground-truth may not match"
  fi
}

if command -v timeout &>/dev/null; then
  TIMEOUT_CMD="timeout"
elif command -v gtimeout &>/dev/null; then
  TIMEOUT_CMD="gtimeout"
else
  TIMEOUT_CMD=""
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

# --- Discover tools and tasks ---

tools=()
for script in "$TOOLS_DIR"/*.sh; do
  [[ -f "$script" ]] || continue
  name="$(basename "$script" .sh)"
  [[ "$name" == "protocol" ]] && continue
  matches_filter "$name" "$FILTER_TOOLS" && tools+=("$name")
done

tasks=()
for taskfile in "$TASKS_DIR"/*.yaml; do
  [[ -f "$taskfile" ]] || continue
  name="$(basename "$taskfile" .yaml)"
  matches_filter "$name" "$FILTER_TASKS" && tasks+=("$name")
done

if [[ ${#tools[@]} -eq 0 ]]; then
  echo "No tools matched filter '$FILTER_TOOLS'" >&2
  exit 1
fi
if [[ ${#tasks[@]} -eq 0 ]]; then
  echo "No tasks matched filter '$FILTER_TASKS'" >&2
  exit 1
fi

# --- Parse task files once and cache ---

TASK_CACHE_DIR=$(mktemp -d)
trap 'rm -rf "$TASK_CACHE_DIR"' EXIT

for task in "${tasks[@]}"; do
  python3 "$LIB_DIR/parse_task.py" "$TASKS_DIR/$task.yaml" > "$TASK_CACHE_DIR/$task.json"
  python3 -c "import sys,json; print(' '.join(json.load(sys.stdin)['repos'].keys()))" \
    < "$TASK_CACHE_DIR/$task.json" > "$TASK_CACHE_DIR/$task.repos"
done

task_repos() {
  cat "$TASK_CACHE_DIR/$1.repos"
}

task_json() {
  cat "$TASK_CACHE_DIR/$1.json"
}

# --- Collect unique repos across all tasks ---

all_repos=()
for task in "${tasks[@]}"; do
  for repo in $(task_repos "$task"); do
    matches_filter "$repo" "$FILTER_REPOS" || continue
    # Deduplicate
    already=false
    for r in "${all_repos[@]+"${all_repos[@]}"}"; do [[ "$r" == "$repo" ]] && already=true; done
    $already || all_repos+=("$repo")
  done
done

# Returns space-separated tasks that apply to a given repo
tasks_for_repo() {
  local repo="$1"
  local result=""
  for task in "${tasks[@]}"; do
    for r in $(task_repos "$task"); do
      if [[ "$r" == "$repo" ]]; then
        result+="$task "
      fi
    done
  done
  echo "$result"
}

# --- Count runs ---

total_runs=0
for tool in "${tools[@]}"; do
  for task in "${tasks[@]}"; do
    for repo in $(task_repos "$task"); do
      matches_filter "$repo" "$FILTER_REPOS" || continue
      total_runs=$((total_runs + 1))
    done
  done
done

log "Evaluation matrix: ${#tools[@]} tools × ${#tasks[@]} tasks = $total_runs runs"
log "Tools: ${tools[*]}"
log "Tasks: ${tasks[*]}"

if $DRY_RUN; then
  echo ""
  echo "=== DRY RUN ==="
  echo ""
  run_num=0
  for tool in "${tools[@]}"; do
    for repo in "${all_repos[@]}"; do
      for task in $(tasks_for_repo "$repo"); do
        run_num=$((run_num + 1))
        rp=$(repo_path "$repo")
        exists="YES"
        [[ -d "$rp" ]] || exists="MISSING"
        echo "  [$run_num/$total_runs] tool=$tool repo=$repo ($exists) task=$task"
      done
    done
  done
  echo ""
  echo "Estimated cost: ~\$$(echo "$total_runs * 0.05" | bc) (at ~\$0.05/session)"
  exit 0
fi

# --- Main loop: tool → repo (setup once) → tasks ---

run_num=0
passed=0
failed=0
skipped=0

for tool in "${tools[@]}"; do
  for repo in "${all_repos[@]}"; do
    # Check repo exists
    rp=$(repo_path "$repo")
    if [[ ! -d "$rp" ]]; then
      for task in $(tasks_for_repo "$repo"); do
        run_num=$((run_num + 1))
        log "[$run_num/$total_runs] tool=$tool repo=$repo task=$task"
        log "  SKIP: repo directory not found: $rp"
        skipped=$((skipped + 1))
      done
      continue
    fi

    check_pinned_commit "$repo" "$rp"

    # Persistent workspace per tool+repo (survives across runs, holds venvs)
    workspace="$RESULTS_DIR/$tool/$repo/.workspace"
    mkdir -p "$workspace"
    first_task="$(tasks_for_repo "$repo" | awk '{print $1}')"
    setup_result_dir="$RESULTS_DIR/$tool/$repo/$first_task"
    mkdir -p "$setup_result_dir"

    # Check if already indexed — skip full setup, just write config
    "$TOOLS_DIR/$tool.sh" --check-ready "$rp" "$workspace" > "$setup_result_dir/index_meta.json" 2>/dev/null && ready_rc=0 || ready_rc=$?
    if [[ $ready_rc -eq 0 ]]; then
      log "  $tool × $repo already indexed — writing config only"
      "$TOOLS_DIR/$tool.sh" --write-config "$rp" "$workspace"
    else
      log "  setting up $tool for $repo (workspace: $workspace)..."
      if ! "$TOOLS_DIR/$tool.sh" "$rp" "$workspace" 2>"$setup_result_dir/setup.log"; then
        log "  FAIL: setup failed (see $setup_result_dir/setup.log)"
        for task in $(tasks_for_repo "$repo"); do
          run_num=$((run_num + 1))
          log "[$run_num/$total_runs] tool=$tool repo=$repo task=$task — setup failed"
          result_dir="$RESULTS_DIR/$tool/$repo/$task"
          mkdir -p "$result_dir"
          echo '{"index_completeness": "setup_failed"}' > "$result_dir/index_meta.json"
          failed=$((failed + 1))
        done
        continue
      fi

      # Poll until ready (once per tool+repo)
      log "  waiting for index readiness..."
      ready=false
      broken=false
      for i in $(seq 1 $READY_POLL_MAX); do
        "$TOOLS_DIR/$tool.sh" --check-ready "$rp" "$workspace" > "$setup_result_dir/index_meta.json" 2>/dev/null && rc=0 || rc=$?
        if [[ $rc -eq 0 ]]; then
          ready=true
          break
        elif [[ $rc -eq 2 ]]; then
          broken=true
          log "  FAIL: tool is broken (exit 2)"
          break
        fi
        if [[ $((i % 12)) -eq 0 ]]; then
          log "  still waiting... ($(( i * READY_POLL_INTERVAL ))s elapsed)"
        fi
        sleep $READY_POLL_INTERVAL
      done

      if [[ "$ready" != "true" ]]; then
        for task in $(tasks_for_repo "$repo"); do
          run_num=$((run_num + 1))
          log "[$run_num/$total_runs] tool=$tool repo=$repo task=$task — index not ready"
          result_dir="$RESULTS_DIR/$tool/$repo/$task"
          mkdir -p "$result_dir"
          if [[ "$broken" == "true" ]]; then
            echo '{"index_completeness": "broken"}' > "$result_dir/index_meta.json"
            failed=$((failed + 1))
          else
            echo '{"index_completeness": "timeout"}' > "$result_dir/index_meta.json"
            skipped=$((skipped + 1))
          fi
        done
        continue
      fi
    fi

    index_meta=$(cat "$setup_result_dir/index_meta.json")
    log "  index ready: $index_meta"

    # Run all tasks for this tool+repo
    claude_md=$(cat "$workspace/CLAUDE.md")
    for task in $(tasks_for_repo "$repo"); do
      run_num=$((run_num + 1))
      log "[$run_num/$total_runs] tool=$tool repo=$repo task=$task"

      result_dir="$RESULTS_DIR/$tool/$repo/$task"
      mkdir -p "$result_dir"

      # Copy index_meta to each task result
      echo "$index_meta" > "$result_dir/index_meta.json"

      # Render prompt
      rendered=$(task_json "$task" | python3 -c "
import sys, json
task = json.load(sys.stdin)
repo = sys.argv[1]
template = task.get('prompt_template', '')
params = task.get('repos', {}).get(repo, {})
for var in task.get('variables', []):
    template = template.replace('{' + var + '}', params.get(var, '{' + var + '}'))
print(json.dumps({'prompt': template, 'params': params, 'scoring': task.get('scoring', {})}))
" "$repo")
      prompt=$(echo "$rendered" | python3 -c "import sys,json; print(json.load(sys.stdin)['prompt'])")

      # Build claude command
      claude_args=(
        -p "$prompt"
        --verbose
        --append-system-prompt "$claude_md"
        --output-format stream-json
        --permission-mode bypassPermissions
        --disallowed-tools "Agent"
        --max-budget-usd "$MAX_BUDGET_USD"
      )

      if [[ -f "$workspace/.mcp.json" ]]; then
        claude_args+=(--mcp-config "$workspace/.mcp.json")
      fi

      # Run Claude session from repo directory
      log "  running Claude session..."
      start_time=$(date +%s)

      (cd "$rp" && run_with_timeout "$SESSION_TIMEOUT" claude "${claude_args[@]}" > "$result_dir/transcript.json" 2>"$result_dir/claude.log") && claude_rc=0 || claude_rc=$?
      end_time=$(date +%s)
      wall_time=$((end_time - start_time))

      # Check if budget-exceeded (transcript has a result line with error_max_budget_usd)
      budget_exceeded=false
      if [[ $claude_rc -ne 0 && -s "$result_dir/transcript.json" ]]; then
        if tail -1 "$result_dir/transcript.json" | grep -q '"error_max_budget_usd"'; then
          budget_exceeded=true
        fi
      fi

      if [[ $claude_rc -eq 0 || "$budget_exceeded" == "true" ]]; then
        if [[ "$budget_exceeded" == "true" ]]; then
          log "  done in ${wall_time}s (budget exceeded — transcript still scorable)"
        else
          log "  done in ${wall_time}s"
        fi

        tool_version=$(grep -m1 '^TOOL_VERSION=' "$TOOLS_DIR/$tool.sh" 2>/dev/null | cut -d'"' -f2 || echo "")
        repo_commit=$(cd "$rp" && git rev-parse --short HEAD 2>/dev/null || echo "")

        python3 -c "
import json, sys
meta = {
    'tool': sys.argv[1],
    'repo': sys.argv[2],
    'task': sys.argv[3],
    'wall_time_seconds': int(sys.argv[4]),
    'max_budget_usd': float(sys.argv[5]),
    'timestamp': sys.argv[6],
    'tool_version': sys.argv[7] or None,
    'repo_commit': sys.argv[8] or None,
    'budget_exceeded': sys.argv[9] == 'true',
}
json.dump(meta, sys.stdout, indent=2)
print()
" "$tool" "$repo" "$task" "$wall_time" "$MAX_BUDGET_USD" "$(timestamp)" "$tool_version" "$repo_commit" "$budget_exceeded" > "$result_dir/run_meta.json"

        passed=$((passed + 1))
      else
        log "  FAIL: Claude session failed after ${wall_time}s (see $result_dir/claude.log)"
        echo "{\"error\": \"claude_session_failed\", \"wall_time_seconds\": $wall_time}" > "$result_dir/run_meta.json"
        failed=$((failed + 1))
      fi
    done

    # Workspace persists at $RESULTS_DIR/$tool/$repo/.workspace for reuse
  done
done

# --- Summary ---

log ""
log "=== Evaluation complete ==="
log "  Total: $total_runs | Passed: $passed | Failed: $failed | Skipped: $skipped"
log "  Results in: $RESULTS_DIR/"
