#!/usr/bin/env bash
set -euo pipefail

# Run the LLM-as-judge over every scored.json under results/.
# Idempotent: skips a result_dir if judged.json already exists and is
# newer than its transcript.json, unless --force is passed. Cached prompts
# are shared per scenario (12 sessions × 4 steps × 1 scenario all hit the
# same system prefix), so a full sweep of one scenario costs ~one cache
# miss + 47 cache hits.

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
BENCH_PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
# Resolves RESULTS_DIR + SCENARIOS_DIR for the global or VERTICAL bench.
source "$BENCH_DIR/lib/bench-paths.sh"
LIB_DIR="$BENCH_DIR/lib"
# shellcheck disable=SC1091
source "$LIB_DIR/load-env.sh"

FILTER_TOOLS=""
FILTER_REPOS=""
FORCE=0

VIA_CLI=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool) FILTER_TOOLS="$2"; shift 2 ;;
    --repo) FILTER_REPOS="$2"; shift 2 ;;
    --force) FORCE=1; shift ;;
    --via-cli)
      # Route judge calls through `claude` CLI subprocess (OAuth subscription)
      # instead of urllib direct API. Useful when the API key has run dry
      # but the local CLI still has subscription credit.
      VIA_CLI=1; shift ;;
    -h|--help)
      echo "Usage: judge.sh [--tool t1,t2] [--repo r1,r2] [--force] [--via-cli]"
      echo ""
      echo "Runs the LLM judge against every scored.json under results/."
      echo "Writes judged.json next to each scored.json."
      echo "Auth: ANTHROPIC_API_KEY (default) or --via-cli for subscription."
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

if [[ "$VIA_CLI" == "1" ]]; then
  export BENCH_JUDGE_VIA_CLI=1
  if ! command -v claude >/dev/null 2>&1; then
    echo "judge.sh --via-cli: claude CLI not found in PATH" >&2
    exit 1
  fi
elif [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "judge.sh: ANTHROPIC_API_KEY not set (use --via-cli for subscription mode)" >&2
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

JUDGE_ONE_OUTCOME=""  # set by judge_one: "judged" | "up_to_date" | "no_scored"

judge_one() {
  local result_dir="$1"
  local rubric="$2"
  local transcript="$result_dir/transcript.json"
  local scored="$result_dir/scored.json"
  local judged="$result_dir/judged.json"

  if [[ ! -f "$scored" ]]; then
    log "SKIP: no scored.json in $result_dir"
    JUDGE_ONE_OUTCOME="no_scored"
    return
  fi

  if [[ "$FORCE" -eq 0 && -f "$judged" && "$judged" -nt "$transcript" ]]; then
    log "SKIP (up-to-date): $result_dir"
    JUDGE_ONE_OUTCOME="up_to_date"
    return
  fi

  log "Judging $result_dir"
  python3 "$LIB_DIR/judge.py" "$scored" "$transcript" "$rubric"
  JUDGE_ONE_OUTCOME="judged"
}

judged_count=0
up_to_date_count=0
no_scored_count=0
no_rubric_count=0

for tool_dir in "$RESULTS_DIR"/*/; do
  [[ -d "$tool_dir" ]] || continue
  tool=$(basename "$tool_dir")
  # "vertical" is the reserved per-vertical subtree, not an arm.
  [[ "$tool" == "vertical" ]] && continue
  matches_filter "$tool" "$FILTER_TOOLS" || continue

  for repo_dir in "$tool_dir"*/; do
    [[ -d "$repo_dir" ]] || continue
    repo=$(basename "$repo_dir")
    matches_filter "$repo" "$FILTER_REPOS" || continue

    rubric=$(find_rubric "$repo")
    if [[ -z "$rubric" ]]; then
      log "SKIP: no rubric for repo $repo (scenarios/${repo}.rubric.yaml)"
      no_rubric_count=$((no_rubric_count + 1))
      continue
    fi

    if [[ -f "$repo_dir/transcript.json" ]]; then
      judge_one "$repo_dir" "$rubric"
      case "$JUDGE_ONE_OUTCOME" in
        judged)     judged_count=$((judged_count + 1)) ;;
        up_to_date) up_to_date_count=$((up_to_date_count + 1)) ;;
        no_scored)  no_scored_count=$((no_scored_count + 1)) ;;
      esac
    fi

    for run_dir in "$repo_dir"/run-*/; do
      [[ -d "$run_dir" ]] || continue
      if [[ -f "$run_dir/transcript.json" ]]; then
        judge_one "$run_dir" "$rubric"
        case "$JUDGE_ONE_OUTCOME" in
          judged)     judged_count=$((judged_count + 1)) ;;
          up_to_date) up_to_date_count=$((up_to_date_count + 1)) ;;
          no_scored)  no_scored_count=$((no_scored_count + 1)) ;;
        esac
      fi
    done
  done
done

log ""
log "Judging complete: $judged_count judged, $up_to_date_count up-to-date, $no_rubric_count missing rubric, $no_scored_count missing scored.json"
if [[ "$judged_count" -eq 0 && "$up_to_date_count" -gt 0 ]]; then
  log "(All cells were already up-to-date. Pass --force to re-judge, or delete specific judged.json files.)"
fi
