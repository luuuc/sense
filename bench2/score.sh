#!/usr/bin/env bash
set -euo pipefail

BENCH2_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$BENCH2_DIR/.." && pwd)"
RESULTS_DIR="$BENCH2_DIR/results"
SCENARIOS_DIR="$BENCH2_DIR/scenarios"
LIB_DIR="$BENCH2_DIR/lib"

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

log() {
  echo "[score] $*" >&2
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

for tool_dir in "$RESULTS_DIR"/*/; do
  [[ -d "$tool_dir" ]] || continue
  tool=$(basename "$tool_dir")
  matches_filter "$tool" "$FILTER_TOOLS" || continue

  for repo_dir in "$tool_dir"*/; do
    [[ -d "$repo_dir" ]] || continue
    repo=$(basename "$repo_dir")
    matches_filter "$repo" "$FILTER_REPOS" || continue

    scenario_file=$(find_scenario "$repo")
    if [[ -z "$scenario_file" ]]; then
      log "SKIP: no scenario for repo $repo"
      continue
    fi

    # Check for main result dir
    if [[ -f "$repo_dir/transcript.json" ]]; then
      log "Scoring $tool/$repo"
      python3 "$LIB_DIR/scorer.py" "$repo_dir" "$scenario_file" "$BENCH2_DIR" "$SENSE_BENCH_ROOT/$tool/$repo"
      scored_count=$((scored_count + 1))
    fi

    # Check for run-N subdirs
    for run_dir in "$repo_dir"/run-*/; do
      [[ -d "$run_dir" ]] || continue
      if [[ -f "$run_dir/transcript.json" ]]; then
        log "Scoring $tool/$repo/$(basename "$run_dir")"
        python3 "$LIB_DIR/scorer.py" "$run_dir" "$scenario_file" "$BENCH2_DIR" "$SENSE_BENCH_ROOT/$tool/$repo"
        scored_count=$((scored_count + 1))
      fi
    done
  done
done

log ""
log "Scoring complete: $scored_count scored"
