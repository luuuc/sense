#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
# Resolves RESULTS_DIR + SCENARIOS_DIR for the global or VERTICAL bench.
source "$BENCH_DIR/lib/bench-paths.sh"
LIB_DIR="$BENCH_DIR/lib"

# Mirror run.sh: tool/repo checkouts live under SENSE_BENCH_ROOT.
# Grounding (20-04) reads the checked-out repo to verify citations.
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"

# --- Argument parsing ---

FILTER_TOOLS=""
FILTER_REPOS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool) FILTER_TOOLS="$2"; shift 2 ;;
    --repo) FILTER_REPOS="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: score.sh [--tool t1,t2] [--repo r1,r2]"
      echo ""
      echo "Scores all transcripts against their scenario checklists."
      echo "Writes scored.json next to each transcript.json."
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

# Stale cells (scored.json missing or older than transcript.json) are always
# re-scored, even when --tool/--repo filters would exclude them — otherwise a
# partial rerun leaves report.md mixing fresh transcripts with old scores.
is_stale() {
  local dir="$1"
  [[ -f "$dir/transcript.json" ]] || return 1
  [[ -f "$dir/scored.json" ]] || return 0
  [[ "$dir/transcript.json" -nt "$dir/scored.json" ]]
}

log() {
  echo "[score] $*" >&2
}

# Resolve the host repo path that scorer.py uses for citation_grounding.
# Host mode populates $SENSE_BENCH_ROOT/$tool/$repo via run.sh's `git clone
# --reference`. Docker mode bakes the repo inside the image and never writes
# to that path, so we fall back to the shared $SENSE_BENCH_ROOT/_reference/$repo
# checkout. Both point at the pinned commit when the bench is consistent.
resolve_repo_checkout() {
  local tool="$1" repo="$2"
  local per_tool="$SENSE_BENCH_ROOT/$tool/$repo"
  local reference="$SENSE_BENCH_ROOT/_reference/$repo"
  if [[ -d "$per_tool/.git" ]]; then
    echo "$per_tool"
  elif [[ -d "$reference/.git" ]]; then
    echo "$reference"
  else
    echo "$per_tool"  # let scorer.py emit its own "repo_path missing" error
  fi
}

# --- Find scenario for a repo ---
find_scenario() {
  local repo="$1"
  for sf in "$SCENARIOS_DIR"/*.yaml; do
    local r
    r=$(python3 -c "import yaml; print(yaml.safe_load(open('$sf'))['repo'])" 2>/dev/null || echo "")
    [[ "$r" == "$repo" ]] && echo "$sf" && return
  done
  echo ""
}

scored_count=0
skipped_count=0

stale_count=0

for tool_dir in "$RESULTS_DIR"/*/; do
  [[ -d "$tool_dir" ]] || continue
  tool=$(basename "$tool_dir")
  # "vertical" is the reserved per-vertical subtree, not an arm; it is scored on
  # its own root (RESULTS_DIR=verticals/<name>/results), never as a global arm.
  [[ "$tool" == "vertical" ]] && continue

  for repo_dir in "$tool_dir"*/; do
    [[ -d "$repo_dir" ]] || continue
    repo=$(basename "$repo_dir")

    in_filter=true
    matches_filter "$tool" "$FILTER_TOOLS" && matches_filter "$repo" "$FILTER_REPOS" || in_filter=false

    scenario_file=$(find_scenario "$repo")
    if [[ -z "$scenario_file" ]]; then
      $in_filter && log "SKIP: no scenario for repo $repo"
      continue
    fi

    # Main result dir
    if [[ -f "$repo_dir/transcript.json" ]]; then
      stale=false
      is_stale "$repo_dir" && stale=true
      if $in_filter || $stale; then
        if $stale && ! $in_filter; then
          log "Scoring $tool/$repo (stale — outside filter)"
          stale_count=$((stale_count + 1))
        else
          log "Scoring $tool/$repo"
        fi
        python3 "$LIB_DIR/scorer.py" "$repo_dir" "$scenario_file" "$BENCH_DIR" "$(resolve_repo_checkout "$tool" "$repo")"
        scored_count=$((scored_count + 1))
      fi
    fi

    # run-N subdirs
    for run_dir in "$repo_dir"/run-*/; do
      [[ -d "$run_dir" ]] || continue
      [[ -f "$run_dir/transcript.json" ]] || continue
      stale=false
      is_stale "$run_dir" && stale=true
      if $in_filter || $stale; then
        if $stale && ! $in_filter; then
          log "Scoring $tool/$repo/$(basename "$run_dir") (stale — outside filter)"
          stale_count=$((stale_count + 1))
        else
          log "Scoring $tool/$repo/$(basename "$run_dir")"
        fi
        python3 "$LIB_DIR/scorer.py" "$run_dir" "$scenario_file" "$BENCH_DIR" "$(resolve_repo_checkout "$tool" "$repo")"
        scored_count=$((scored_count + 1))
      fi
    done
  done
done

log ""
if [[ $stale_count -gt 0 ]]; then
  log "Scoring complete: $scored_count scored ($stale_count stale cells outside filter re-scored)"
else
  log "Scoring complete: $scored_count scored"
fi
