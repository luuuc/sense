#!/usr/bin/env bash
set -euo pipefail

# Run the LLM-as-judge over every scored.json under results/.
# Idempotent: skips a result_dir if judged.json already exists and is
# newer than its transcript.json, unless --force is passed. Cached prompts
# are shared per scenario (12 sessions × 4 steps × 1 scenario all hit the
# same system prefix), so a full sweep of one scenario costs ~one cache
# miss + 47 cache hits.

BENCH2_DIR="$(cd "$(dirname "$0")" && pwd)"
BENCH2_PROJECT_ROOT="$(cd "$BENCH2_DIR/.." && pwd)"
RESULTS_DIR="$BENCH2_DIR/results"
SCENARIOS_DIR="$BENCH2_DIR/scenarios"
LIB_DIR="$BENCH2_DIR/lib"
# shellcheck disable=SC1091
source "$LIB_DIR/load-env.sh"

FILTER_TOOLS=""
FILTER_REPOS=""
FORCE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool) FILTER_TOOLS="$2"; shift 2 ;;
    --repo) FILTER_REPOS="$2"; shift 2 ;;
    --force) FORCE=1; shift ;;
    -h|--help)
      echo "Usage: judge.sh [--tool t1,t2] [--repo r1,r2] [--force]"
      echo ""
      echo "Runs the LLM judge against every scored.json under results/."
      echo "Writes judged.json next to each scored.json. Requires"
      echo "ANTHROPIC_API_KEY in env."
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "judge.sh: ANTHROPIC_API_KEY not set" >&2
  exit 1
fi

matches_filter() {
  local value="$1" filter="$2"
  [[ -z "$filter" ]] && return 0
  echo "$filter" | tr ',' '\n' | grep -qx "$value"
}

log() {
  echo "[judge] $*" >&2
}

find_rubric() {
  local repo="$1"
  local rubric="$SCENARIOS_DIR/${repo}.rubric.yaml"
  [[ -f "$rubric" ]] && echo "$rubric"
}

judge_one() {
  local result_dir="$1"
  local rubric="$2"
  local transcript="$result_dir/transcript.json"
  local scored="$result_dir/scored.json"
  local judged="$result_dir/judged.json"

  if [[ ! -f "$scored" ]]; then
    log "SKIP: no scored.json in $result_dir"
    return
  fi

  if [[ "$FORCE" -eq 0 && -f "$judged" && "$judged" -nt "$transcript" ]]; then
    log "SKIP (up-to-date): $result_dir"
    return
  fi

  log "Judging $result_dir"
  python3 "$LIB_DIR/judge.py" "$scored" "$transcript" "$rubric"
}

judged_count=0
skipped_count=0

for tool_dir in "$RESULTS_DIR"/*/; do
  [[ -d "$tool_dir" ]] || continue
  tool=$(basename "$tool_dir")
  matches_filter "$tool" "$FILTER_TOOLS" || continue

  for repo_dir in "$tool_dir"*/; do
    [[ -d "$repo_dir" ]] || continue
    repo=$(basename "$repo_dir")
    matches_filter "$repo" "$FILTER_REPOS" || continue

    rubric=$(find_rubric "$repo")
    if [[ -z "$rubric" ]]; then
      log "SKIP: no rubric for repo $repo (scenarios/${repo}.rubric.yaml)"
      skipped_count=$((skipped_count + 1))
      continue
    fi

    if [[ -f "$repo_dir/transcript.json" ]]; then
      judge_one "$repo_dir" "$rubric"
      judged_count=$((judged_count + 1))
    fi

    for run_dir in "$repo_dir"/run-*/; do
      [[ -d "$run_dir" ]] || continue
      if [[ -f "$run_dir/transcript.json" ]]; then
        judge_one "$run_dir" "$rubric"
        judged_count=$((judged_count + 1))
      fi
    done
  done
done

log ""
log "Judging complete: $judged_count judged, $skipped_count skipped (missing rubric)"
