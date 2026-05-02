#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
TOOLS_DIR="$BENCH_DIR/tools"
TASKS_DIR="$BENCH_DIR/tasks"
RESULTS_DIR="$BENCH_DIR/results"
LIB_DIR="$BENCH_DIR/lib"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"
REF_DIR="$SENSE_BENCH_ROOT/_reference"
PINNED_COMMITS="$BENCH_DIR/PINNED_COMMITS.json"
READY_POLL_INTERVAL=5
READY_POLL_MAX=720  # 60 minutes at 5s intervals
SETUP_TIMEOUT=600   # 10 minutes max for initial setup

# --- Argument parsing ---

FILTER_TOOLS=""
FILTER_REPOS=""
RUN_REPORT=false
FORCE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool)   FILTER_TOOLS="$2"; shift 2 ;;
    --repo)   FILTER_REPOS="$2"; shift 2 ;;
    --report) RUN_REPORT=true; shift ;;
    --force)   FORCE=true; shift ;;
    --timeout) SETUP_TIMEOUT="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: scan.sh [--tool t1,t2] [--repo r1,r2] [--force] [--timeout N] [--report]"
      echo ""
      echo "Scan all tool × repo pairs, measuring cold-start timing."
      echo "Skips already-indexed repos unless --force is passed."
      echo ""
      echo "Options:"
      echo "  --tool      Comma-separated tool filter (e.g. sense,grepai)"
      echo "  --repo      Comma-separated repo filter (e.g. flask,discourse)"
      echo "  --force     Delete existing indexes before scanning"
      echo "  --timeout N Max seconds for setup per tool×repo (default: 600)"
      echo "  --report    Run report.sh after all scans complete"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

# --- Helpers ---

# Portable timeout (macOS has no coreutils timeout)
run_with_timeout() {
  local secs="$1"; shift
  "$@" &
  local pid=$!
  ( sleep "$secs" && kill "$pid" 2>/dev/null ) &
  local watcher=$!
  wait "$pid" 2>/dev/null
  local rc=$?
  kill "$watcher" 2>/dev/null
  wait "$watcher" 2>/dev/null
  return $rc
}

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
    log "  Run: bash bench/bootstrap-repos.sh --repo $repo"
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
" "$PINNED_COMMITS" "$repo")
  if [[ -n "$pinned" ]]; then
    (cd "$dest" && git -c advice.detachedHead=false checkout "$pinned" --quiet)
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
    [[ "$name" == "protocol" ]] && continue
    tools+=("$name")
  done
fi

# --- Parse task files once and cache first-task-per-repo ---

TASK_CACHE_DIR=$(mktemp -d)
trap 'rm -rf "$TASK_CACHE_DIR"' EXIT

for taskfile in "$TASKS_DIR"/*.yaml; do
  [[ -f "$taskfile" ]] || continue
  task="$(basename "$taskfile" .yaml)"
  python3 "$LIB_DIR/parse_task.py" "$taskfile" > "$TASK_CACHE_DIR/$task.json"
  for repo in $(python3 -c "import sys,json; print(' '.join(json.load(sys.stdin)['repos'].keys()))" < "$TASK_CACHE_DIR/$task.json"); do
    if [[ ! -f "$TASK_CACHE_DIR/_first_task_$repo" ]]; then
      echo "$task" > "$TASK_CACHE_DIR/_first_task_$repo"
    fi
  done
done

# --- Discover repos from cached task data ---

all_repos=()
for task_json in "$TASK_CACHE_DIR"/*.json; do
  [[ -f "$task_json" ]] || continue
  for repo in $(python3 -c "import sys,json; print(' '.join(json.load(sys.stdin)['repos'].keys()))" < "$task_json"); do
    matches_filter "$repo" "$FILTER_REPOS" || continue
    already=false
    for r in "${all_repos[@]+"${all_repos[@]}"}"; do [[ "$r" == "$repo" ]] && already=true; done
    $already || all_repos+=("$repo")
  done
done

if [[ ${#tools[@]} -eq 0 ]]; then
  echo "No tools matched filter '$FILTER_TOOLS'" >&2
  exit 1
fi
if [[ ${#all_repos[@]} -eq 0 ]]; then
  echo "No repos matched filter '$FILTER_REPOS'" >&2
  exit 1
fi

# --- Summary table state ---

declare -a SUMMARY_TOOLS=()
declare -a SUMMARY_REPOS=()
declare -a SUMMARY_TIMES=()
declare -a SUMMARY_EMBEDS=()
declare -a SUMMARY_FILES=()
declare -a SUMMARY_SYMBOLS=()
declare -a SUMMARY_STATUS=()

# --- Suppress Sense hook injection for all tools (each tool gets MCP via --mcp-config) ---
export SENSE_BENCH=1

# --- Main loop ---

total=$((${#tools[@]} * ${#all_repos[@]}))
num=0
scan_ok=0
scan_fail=0

force_label=""; $FORCE && force_label=" (force)"
log "Scan: ${#tools[@]} tools × ${#all_repos[@]} repos = $total scans$force_label"

for tool in "${tools[@]}"; do
  for repo in "${all_repos[@]}"; do
    num=$((num + 1))

    if ! ensure_tool_repo "$tool" "$repo"; then
      log "[$num/$total] $tool × $repo — SKIP (reference repo not available)"
      SUMMARY_TOOLS+=("$tool"); SUMMARY_REPOS+=("$repo"); SUMMARY_TIMES+=("—")
      SUMMARY_EMBEDS+=("—"); SUMMARY_FILES+=("—"); SUMMARY_SYMBOLS+=("—"); SUMMARY_STATUS+=("SKIP")
      scan_fail=$((scan_fail + 1))
      continue
    fi

    rp=$(tool_repo_path "$tool" "$repo")
    workspace="$SENSE_BENCH_ROOT/$tool/$repo/.workspace"
    mkdir -p "$workspace"

    # Skip if already indexed (unless --force)
    if ! $FORCE; then
      "$TOOLS_DIR/$tool.sh" --check-ready "$rp" "$workspace" >/dev/null 2>/dev/null && already_indexed=true || already_indexed=false
      if $already_indexed; then
        log "[$num/$total] $tool × $repo — already indexed, skipping"
        SUMMARY_TOOLS+=("$tool"); SUMMARY_REPOS+=("$repo"); SUMMARY_TIMES+=("—")
        SUMMARY_EMBEDS+=("—"); SUMMARY_FILES+=("—"); SUMMARY_SYMBOLS+=("—"); SUMMARY_STATUS+=("CACHED")
        scan_ok=$((scan_ok + 1))
        continue
      fi
    else
      log "[$num/$total] Deleting $tool index for $repo..."
      case "$tool" in
        codebase-memory-mcp) rm -rf "$workspace/.cbm-cache" ;;
        gitnexus)  rm -rf "$rp/.gitnexus" ;;
        grepai)    rm -rf "$rp/.grepai" ;;
        roam)      rm -rf "$rp/.roam" ;;
        sense)     rm -rf "$rp/.sense" ;;
        tokensave) rm -rf "$rp/.tokensave" ;;
      esac
      rm -rf "$workspace"
      mkdir -p "$workspace"
    fi

    log "[$num/$total] Scanning $tool × $repo..."
    scan_start=$(date +%s)

    first_task=""
    [[ -f "$TASK_CACHE_DIR/_first_task_$repo" ]] && first_task=$(cat "$TASK_CACHE_DIR/_first_task_$repo")
    if [[ -z "$first_task" ]]; then
      log "  SKIP: no task references repo '$repo'"
      SUMMARY_TOOLS+=("$tool"); SUMMARY_REPOS+=("$repo"); SUMMARY_TIMES+=("—")
      SUMMARY_EMBEDS+=("—"); SUMMARY_FILES+=("—"); SUMMARY_SYMBOLS+=("—"); SUMMARY_STATUS+=("SKIP")
      scan_fail=$((scan_fail + 1))
      continue
    fi
    result_dir="$RESULTS_DIR/$tool/$repo/$first_task"
    mkdir -p "$result_dir"

    if ! run_with_timeout "$SETUP_TIMEOUT" "$TOOLS_DIR/$tool.sh" "$rp" "$workspace" 2>"$result_dir/setup.log"; then
      scan_end=$(date +%s)
      elapsed=$((scan_end - scan_start))
      if [[ $elapsed -ge $SETUP_TIMEOUT ]]; then
        log "  FAIL: setup timed out after ${elapsed}s (limit: ${SETUP_TIMEOUT}s)"
      else
        log "  FAIL: setup failed after ${elapsed}s (see $result_dir/setup.log)"
      fi
      SUMMARY_TOOLS+=("$tool"); SUMMARY_REPOS+=("$repo"); SUMMARY_TIMES+=("${elapsed}s")
      SUMMARY_EMBEDS+=("—"); SUMMARY_FILES+=("—"); SUMMARY_SYMBOLS+=("—"); SUMMARY_STATUS+=("FAIL")
      scan_fail=$((scan_fail + 1))
      continue
    fi

    # Poll until ready (handles deferred embeddings)
    ready=false
    broken=false
    for i in $(seq 1 $READY_POLL_MAX); do
      "$TOOLS_DIR/$tool.sh" --check-ready "$rp" "$workspace" > "$result_dir/index_meta.json" 2>/dev/null && rc=0 || rc=$?
      if [[ $rc -eq 0 ]]; then
        ready=true
        break
      elif [[ $rc -eq 2 ]]; then
        broken=true
        break
      fi
      if [[ $((i % 12)) -eq 0 ]]; then
        log "  still waiting... ($(( i * READY_POLL_INTERVAL ))s elapsed)"
      fi
      sleep $READY_POLL_INTERVAL
    done

    scan_end=$(date +%s)
    elapsed=$((scan_end - scan_start))

    if [[ "$ready" != "true" ]]; then
      if [[ "$broken" == "true" ]]; then
        log "  FAIL: tool broken after ${elapsed}s"
      else
        log "  FAIL: timeout after ${elapsed}s"
      fi
      SUMMARY_TOOLS+=("$tool"); SUMMARY_REPOS+=("$repo"); SUMMARY_TIMES+=("${elapsed}s")
      SUMMARY_EMBEDS+=("—"); SUMMARY_FILES+=("—"); SUMMARY_SYMBOLS+=("—"); SUMMARY_STATUS+=("FAIL")
      scan_fail=$((scan_fail + 1))
      continue
    fi

    # Extract metadata from check-ready JSON
    meta_files=$(python3 -c "import sys,json; d=json.load(open(sys.argv[1])); print(d.get('files','—'))" "$result_dir/index_meta.json" 2>/dev/null || echo "—")
    meta_symbols=$(python3 -c "import sys,json; d=json.load(open(sys.argv[1])); print(d.get('symbols','—'))" "$result_dir/index_meta.json" 2>/dev/null || echo "—")

    # Read embedding mode from tool's setup metadata
    embed_mode="—"
    if [[ -f "$workspace/index_meta_setup.json" ]]; then
      deferred=$(python3 -c "import sys,json; print(json.load(open(sys.argv[1])).get('deferred_embeddings', False))" "$workspace/index_meta_setup.json" 2>/dev/null || echo "")
      if [[ "$deferred" == "True" ]]; then
        embed_mode="deferred"
      elif [[ "$deferred" == "False" ]]; then
        embed_mode="included"
      fi
    fi

    # Write the total wall-clock time (setup + poll) back to index_meta_setup.json
    if [[ -f "$workspace/index_meta_setup.json" ]]; then
      python3 -c "
import sys, json
p = sys.argv[1]
d = json.load(open(p))
d['total_wall_seconds'] = int(sys.argv[2])
json.dump(d, open(p, 'w'), indent=2)
print()
" "$workspace/index_meta_setup.json" "$elapsed"
      cp "$workspace/index_meta_setup.json" "$result_dir/index_meta_setup.json"
    fi

    log "  Done: ${elapsed}s (embeddings: $embed_mode)"

    SUMMARY_TOOLS+=("$tool"); SUMMARY_REPOS+=("$repo"); SUMMARY_TIMES+=("${elapsed}s")
    SUMMARY_EMBEDS+=("$embed_mode"); SUMMARY_FILES+=("$meta_files"); SUMMARY_SYMBOLS+=("$meta_symbols")
    SUMMARY_STATUS+=("OK")
    scan_ok=$((scan_ok + 1))
  done
done

# --- Summary table ---

echo ""
echo "════════════════════════════════════════════════════════════"
echo "  SCAN TIMING SUMMARY"
echo "════════════════════════════════════════════════════════════"
echo ""

printf "  %-16s %-16s %7s  %-10s %7s %9s  %s\n" "Tool" "Repo" "Time" "Embeddings" "Files" "Symbols" "Status"
printf "  %-16s %-16s %7s  %-10s %7s %9s  %s\n" "──────────────" "──────────────" "─────" "──────────" "─────" "───────" "──────"

for i in "${!SUMMARY_TOOLS[@]}"; do
  printf "  %-16s %-16s %7s  %-10s %7s %9s  %s\n" \
    "${SUMMARY_TOOLS[$i]}" "${SUMMARY_REPOS[$i]}" "${SUMMARY_TIMES[$i]}" \
    "${SUMMARY_EMBEDS[$i]}" "${SUMMARY_FILES[$i]}" "${SUMMARY_SYMBOLS[$i]}" "${SUMMARY_STATUS[$i]}"
done

echo ""
echo "  Total: $total | OK: $scan_ok | Failed: $scan_fail"
echo "  Results in: $RESULTS_DIR/"
echo ""

# --- Optional report ---

if $RUN_REPORT; then
  log "Running report.sh..."
  exec "$BENCH_DIR/report.sh"
fi
