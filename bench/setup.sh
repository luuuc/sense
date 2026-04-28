#!/usr/bin/env bash
set -euo pipefail

# Setup tool indexes for benchmark repos.
#
# Run this once (or after adding repos/tools). Indexes persist in repo dirs
# (.sense/, .roam/, .code-review-graph/, .grepai/, .tokensave/).
# run.sh skips setup when the tool is already indexed.

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
TOOLS_DIR="$BENCH_DIR/tools"
TASKS_DIR="$BENCH_DIR/tasks"
RESULTS_DIR="$BENCH_DIR/results"
LIB_DIR="$BENCH_DIR/lib"
REPOS_DIR="$BENCH_DIR/repos"
# --- Argument parsing ---

FILTER_TOOLS=""
FILTER_REPOS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool) FILTER_TOOLS="$2"; shift 2 ;;
    --repo) FILTER_REPOS="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: setup.sh [--tool t1,t2] [--repo r1,r2]"
      echo ""
      echo "Indexes all tool × repo pairs. Run once; run.sh skips setup when indexed."
      echo ""
      echo "Options:"
      echo "  --tool  Comma-separated tool filter (e.g. sense,roam)"
      echo "  --repo  Comma-separated repo filter (e.g. sense,discourse)"
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
  echo "$REPOS_DIR/$repo"
}

timestamp() {
  date +%Y-%m-%dT%H:%M:%S
}

log() {
  echo "[$(timestamp)] $*" >&2
}

# --- Discover tools ---

tools=()
for script in "$TOOLS_DIR"/*.sh; do
  [[ -f "$script" ]] || continue
  name="$(basename "$script" .sh)"
  [[ "$name" == "protocol" ]] && continue
  matches_filter "$name" "$FILTER_TOOLS" && tools+=("$name")
done

# --- Discover repos from task files ---

tasks=()
for taskfile in "$TASKS_DIR"/*.yaml; do
  [[ -f "$taskfile" ]] || continue
  tasks+=("$(basename "$taskfile" .yaml)")
done

TASK_CACHE_DIR=$(mktemp -d)
trap 'rm -rf "$TASK_CACHE_DIR"' EXIT

for task in "${tasks[@]}"; do
  python3 "$LIB_DIR/parse_task.py" "$TASKS_DIR/$task.yaml" > "$TASK_CACHE_DIR/$task.json"
  python3 -c "import sys,json; print(' '.join(json.load(sys.stdin)['repos'].keys()))" \
    < "$TASK_CACHE_DIR/$task.json" > "$TASK_CACHE_DIR/$task.repos"
done

all_repos=()
for task in "${tasks[@]}"; do
  for repo in $(cat "$TASK_CACHE_DIR/$task.repos"); do
    matches_filter "$repo" "$FILTER_REPOS" || continue
    already=false
    for r in "${all_repos[@]+"${all_repos[@]}"}"; do [[ "$r" == "$repo" ]] && already=true; done
    $already || all_repos+=("$repo")
  done
done

# --- Setup each tool × repo ---

total=$((${#tools[@]} * ${#all_repos[@]}))
num=0
setup_ok=0
setup_fail=0
already_ready=0

for tool in "${tools[@]}"; do
  for repo in "${all_repos[@]}"; do
    num=$((num + 1))
    rp=$(repo_path "$repo")

    if [[ ! -d "$rp" ]]; then
      log "[$num/$total] $tool × $repo — SKIP (repo not found: $rp)"
      continue
    fi

    # Check if already ready (use a throwaway workspace for tools that need it)
    workspace="$RESULTS_DIR/$tool/$repo/.workspace"
    mkdir -p "$workspace"

    "$TOOLS_DIR/$tool.sh" --check-ready "$rp" "$workspace" >/dev/null 2>/dev/null && rc=0 || rc=$?
    if [[ $rc -eq 0 ]]; then
      log "[$num/$total] $tool × $repo — already indexed, skipping"
      already_ready=$((already_ready + 1))
      continue
    fi

    log "[$num/$total] $tool × $repo — setting up..."
    setup_log="$RESULTS_DIR/$tool/$repo/setup.log"
    mkdir -p "$(dirname "$setup_log")"

    if ! "$TOOLS_DIR/$tool.sh" "$rp" "$workspace" 2>"$setup_log"; then
      log "  FAIL: setup failed (see $setup_log)"
      setup_fail=$((setup_fail + 1))
      continue
    fi

    # Check if already ready (indexing may still be in progress — that's OK,
    # run.sh polls before Claude sessions)
    "$TOOLS_DIR/$tool.sh" --check-ready "$rp" "$workspace" >/dev/null 2>/dev/null && rc=0 || rc=$?
    if [[ $rc -eq 0 ]]; then
      log "  done (indexed)"
    else
      log "  done (indexing in background — run.sh will wait)"
    fi
    setup_ok=$((setup_ok + 1))
  done
done

log ""
log "=== Setup complete ==="
log "  Total: $total | Indexed: $setup_ok | Already ready: $already_ready | Failed: $setup_fail"
