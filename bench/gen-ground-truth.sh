#!/usr/bin/env bash
set -euo pipefail

# Generate ground truth from grep/static analysis — NOT from Sense.
#
# Usage:
#   bash bench/gen-ground-truth.sh --repo flask --task callers
#   bash bench/gen-ground-truth.sh --repo flask              # all tasks for flask
#   bash bench/gen-ground-truth.sh                            # all repos, all tasks
#
# Writes to ground-truth/<repo>/<task>.json with status "verified"
# and a _note explaining how it was generated.

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
GT_DIR="$BENCH_DIR/ground-truth"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"
REPOS_DIR="$SENSE_BENCH_ROOT/_reference"
TASKS_DIR="$BENCH_DIR/tasks"
LIB_DIR="$BENCH_DIR/lib"
PINNED="$BENCH_DIR/PINNED_COMMITS.json"

FILTER_REPOS=""
FILTER_TASKS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) FILTER_REPOS="$2"; shift 2 ;;
    --task) FILTER_TASKS="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: gen-ground-truth.sh [--repo r1,r2] [--task t1,t2]"
      echo ""
      echo "Generates ground truth from grep/static analysis (not from Sense)."
      echo "Writes to ground-truth/<repo>/<task>.json"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

matches_filter() {
  local value="$1" filter="$2"
  [[ -z "$filter" ]] && return 0
  echo "$filter" | tr ',' '\n' | grep -qx "$value"
}

log() { echo "[gen-gt] $*" >&2; }

repo_language() {
  local repo="$1"
  python3 -c "
import json,sys
v = json.load(open(sys.argv[1])).get(sys.argv[2], {})
if isinstance(v, dict): print(v.get('language','unknown'))
else: print('unknown')
" "$PINNED" "$repo"
}

repo_commit() {
  local rp="$1"
  (cd "$rp" && git rev-parse --short HEAD 2>/dev/null || echo "unknown")
}

# --- Per-repo generator dispatch ---

gen_scored_task() {
  local task="$1" repo="$2" rp="$3" var_value="$4" lang="$5" commit="$6"

  # Map task name to Python module name (blast-radius -> blast_radius)
  local module_name="${task//-/_}"
  local generator="$LIB_DIR/gt/$repo/${module_name}.py"

  if [[ ! -f "$generator" ]]; then
    log "  ERROR: no generator at $generator"
    return 1
  fi

  local outfile="$GT_DIR/$repo/$task.json"
  mkdir -p "$(dirname "$outfile")"

  log "  $task: running $generator..."
  python3 "$generator" "$rp" "$var_value" "$lang" "$commit" "$repo" > "$outfile"

  # Extract and log count
  local match_key=""
  case "$task" in
    callers) match_key="callers" ;;
    blast-radius) match_key="affected" ;;
    dead-code) match_key="dead_symbols" ;;
  esac

  if [[ -n "$match_key" ]]; then
    local count
    count=$(python3 -c "import json; print(len(json.load(open('$outfile')).get('$match_key',[])))")
    log "  $task: found $count entries → $outfile"
  fi
}

# --- Semantic search: human-curated (cannot be grep-generated) ---

gen_semantic_search() {
  local repo="$1" rp="$2"
  local outfile="$GT_DIR/$repo/semantic-search.json"

  if [[ -f "$outfile" ]]; then
    local existing_gen
    existing_gen=$(python3 -c "import json; print(json.load(open('$outfile')).get('_generated_by',''))" 2>/dev/null || echo "")
    if [[ "$existing_gen" != "gen-ground-truth.sh" ]]; then
      log "  semantic-search: keeping existing hand-curated ground truth"
      return
    fi
  fi
  log "  semantic-search: SKIPPED — requires human curation, not generatable by grep"
}

# --- Qualitative tasks: keep existing keywords (not generatable) ---

gen_qualitative() {
  local task="$1" repo="$2"
  local outfile="$GT_DIR/$repo/$task.json"
  if [[ -f "$outfile" ]]; then
    log "  $task: keeping existing keywords (qualitative — not generatable by grep)"
  else
    log "  $task: no ground truth file exists — create manually"
  fi
}

# --- Main ---
FAILURES=0

for taskfile in "$TASKS_DIR"/*.yaml; do
  task=$(basename "$taskfile" .yaml)
  matches_filter "$task" "$FILTER_TASKS" || continue

  task_json=$(python3 "$LIB_DIR/parse_task.py" "$taskfile")

  for repo in $(echo "$task_json" | python3 -c "import sys,json; print(' '.join(json.load(sys.stdin).get('repos',{}).keys()))"); do
    matches_filter "$repo" "$FILTER_REPOS" || continue

    rp="$REPOS_DIR/$repo"
    if [[ ! -d "$rp" ]]; then
      log "SKIP $repo/$task — repo not cloned"
      continue
    fi

    lang=$(repo_language "$repo")
    commit=$(repo_commit "$rp")
    log "$repo/$task (lang=$lang, commit=$commit)"

    # Get task-specific variable
    var_value=$(echo "$task_json" | python3 -c "
import sys,json
t=json.load(sys.stdin)
params=t.get('repos',{}).get('$repo',{})
for v in t.get('variables',[]):
  print(params.get(v,''))
  break
else:
  print('')
")

    case "$task" in
      callers|blast-radius|dead-code)
        gen_scored_task "$task" "$repo" "$rp" "$var_value" "$lang" "$commit" || FAILURES=$((FAILURES+1))
        ;;
      semantic-search)
        gen_semantic_search "$repo" "$rp"
        ;;
      *)
        gen_qualitative "$task" "$repo"
        ;;
    esac
  done
done

log ""
if [[ "$FAILURES" -gt 0 ]]; then
  log "FAILED: $FAILURES ground truth generation(s) failed."
  log "Review the errors above."
  exit 1
fi
log "Done. Review generated files in $GT_DIR/"
log "Semantic-search ground truth must be curated manually."
