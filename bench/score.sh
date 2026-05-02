#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="$BENCH_DIR/results"
LIB_DIR="$BENCH_DIR/lib"

# --- Argument parsing ---

FILTER_TOOLS=""
FILTER_REPOS=""
FILTER_TASKS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool)  FILTER_TOOLS="$2"; shift 2 ;;
    --repo)  FILTER_REPOS="$2"; shift 2 ;;
    --task)  FILTER_TASKS="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: score.sh [--tool t1,t2] [--repo r1,r2] [--task t1,t2]"
      echo ""
      echo "Scores benchmark transcripts against ground truth."
      echo "Reads from results/<tool>/<repo>/<task>/transcript.json"
      echo "Writes to results/<tool>/<repo>/<task>/scored.json"
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

timestamp() {
  date +%Y-%m-%dT%H:%M:%S
}

log() {
  echo "[$(timestamp)] $*" >&2
}

# --- Discover results ---

scored=0
errors=0
skipped=0

for tool_dir in "$RESULTS_DIR"/*/; do
  [[ -d "$tool_dir" ]] || continue
  tool=$(basename "$tool_dir")
  matches_filter "$tool" "$FILTER_TOOLS" || continue

  for repo_dir in "$tool_dir"/*/; do
    [[ -d "$repo_dir" ]] || continue
    repo=$(basename "$repo_dir")
    matches_filter "$repo" "$FILTER_REPOS" || continue

    for task_dir in "$repo_dir"/*/; do
      [[ -d "$task_dir" ]] || continue
      task=$(basename "$task_dir")
      matches_filter "$task" "$FILTER_TASKS" || continue

      # Score transcript in the task dir (single-run) or in run-N subdirs (multi-run)
      score_dir() {
        local dir="$1" label="$2"
        local transcript="$dir/transcript.json"
        if [[ ! -f "$transcript" ]]; then
          skipped=$((skipped + 1))
          return
        fi
        log "Scoring: $label"
        if python3 "$LIB_DIR/scorer.py" "$dir" "$BENCH_DIR" "$tool" "$repo" "$task" > "$dir/scored.json" 2>"$dir/score.log"; then
          scored=$((scored + 1))
        else
          log "  ERROR: scoring failed (see $dir/score.log)"
          errors=$((errors + 1))
        fi
      }

      if [[ -f "$task_dir/transcript.json" ]]; then
        score_dir "$task_dir" "tool=$tool repo=$repo task=$task"
      fi
      for run_dir in "$task_dir"/run-*/; do
        [[ -d "$run_dir" ]] || continue
        run_id=$(basename "$run_dir")
        score_dir "$run_dir" "tool=$tool repo=$repo task=$task $run_id"
      done
    done
  done
done

log ""
log "=== Scoring complete ==="
log "  Scored: $scored | Errors: $errors | Skipped (no transcript): $skipped"

# Regenerate the report if any transcripts were scored
if [[ $scored -gt 0 ]]; then
  log "Regenerating results/report.md ..."
  "$BENCH_DIR/report.sh" --md
fi
