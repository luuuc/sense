#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"

# Smoke profile: 3 repos × 3 scored tasks × all tools
SMOKE_REPOS="gin,flask,nextjs"
SMOKE_TASKS="callers,blast-radius,dead-code"

usage() {
  echo "Usage: quick.sh [single|smoke] [options]"
  echo ""
  echo "Modes:"
  echo "  single REPO TASK TOOL   Run one combination (default: gin conventions sense)"
  echo "  smoke [--tool t1,t2]    Run smoke profile: 3 repos × 3 tasks × tools"
  echo ""
  echo "Smoke profile covers gin (Go/small), flask (Python/medium), nextjs (TS/large)"
  echo "across the 3 set_match tasks (callers, blast-radius, dead-code)."
}

run_single() {
  local repo="$1" task="$2" tool="$3"
  echo "--- $tool × $repo / $task ---"
  local t0
  t0=$(date +%s)

  bash "$BENCH_DIR/run.sh" --tool "$tool" --repo "$repo" --task "$task" --budget 1.00 --timeout 120
  bash "$BENCH_DIR/score.sh" --tool "$tool" --repo "$repo" --task "$task"

  local t1
  t1=$(date +%s)
  local wall=$((t1 - t0))

  local result_dir="$BENCH_DIR/results/$tool/$repo/$task"
  if [[ -f "$result_dir/scored.json" ]]; then
    python3 -c "
import json, sys
scored = json.load(open(sys.argv[1]))
score = scored.get('score', scored.get('correctness_score', 'N/A'))
print(f'  Score: {score}')
metrics = scored.get('metrics', {})
if metrics:
    parts = []
    if 'tool_calls' in metrics: parts.append(f\"tools={metrics['tool_calls']}\")
    if 'token_input' in metrics: parts.append(f\"in={metrics['token_input']}\")
    if 'token_output' in metrics: parts.append(f\"out={metrics['token_output']}\")
    if 'wall_time' in metrics: parts.append(f\"time={metrics['wall_time']}s\")
    print(f'  Metrics: {\" | \".join(parts)}')
" "$result_dir/scored.json"
  else
    echo "  NO SCORE (transcript or scoring failed) — ${wall}s"
    [[ -f "$result_dir/score.log" ]] && tail -3 "$result_dir/score.log"
  fi
  echo ""
}

print_summary() {
  echo ""
  echo "=============================="
  echo "  SMOKE SUMMARY"
  echo "=============================="
  python3 -c "
import json, os, sys

bench = sys.argv[1]
repos = sys.argv[2].split(',')
tasks = sys.argv[3].split(',')
tool_filter = sys.argv[4] if len(sys.argv) > 4 and sys.argv[4] else ''

results_dir = os.path.join(bench, 'results')
tools = sorted(tool_filter.split(',')) if tool_filter else sorted(
    d for d in os.listdir(results_dir)
    if os.path.isdir(os.path.join(results_dir, d))
)

header = f'{\"tool\":<22}'
for repo in repos:
    for task in tasks:
        label = f'{repo[:3]}/{task[:5]}'
        header += f' {label:>10}'
header += f' {\"avg\":>7}'
print(header)
print('-' * len(header))

for tool in tools:
    row = f'{tool:<22}'
    scores = []
    for repo in repos:
        for task in tasks:
            scored_path = os.path.join(results_dir, tool, repo, task, 'scored.json')
            if os.path.exists(scored_path):
                scored = json.load(open(scored_path))
                s = scored.get('score', scored.get('correctness_score'))
                if s is not None:
                    scores.append(float(s))
                    row += f' {float(s):>10.2f}'
                else:
                    row += f' {\"—\":>10}'
            else:
                row += f' {\"—\":>10}'
    avg = sum(scores) / len(scores) if scores else 0
    row += f' {avg:>7.2f}'
    print(row)
" "$BENCH_DIR" "$SMOKE_REPOS" "$SMOKE_TASKS" "${FILTER_TOOLS:-}"
}

# --- Main ---

MODE="${1:-}"

case "$MODE" in
  smoke)
    shift
    FILTER_TOOLS=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --tool) FILTER_TOOLS="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; exit 1 ;;
      esac
    done

    IFS=',' read -ra repos <<< "$SMOKE_REPOS"
    IFS=',' read -ra tasks <<< "$SMOKE_TASKS"

    if [[ -n "$FILTER_TOOLS" ]]; then
      IFS=',' read -ra tools <<< "$FILTER_TOOLS"
    else
      tools=()
      for f in "$BENCH_DIR"/tools/*.sh; do
        tools+=("$(basename "$f" .sh)")
      done
    fi

    total=$(( ${#tools[@]} * ${#repos[@]} * ${#tasks[@]} ))
    echo "=== smoke bench: ${#tools[@]} tools × ${#repos[@]} repos × ${#tasks[@]} tasks = $total runs ==="
    echo "    repos: ${SMOKE_REPOS}"
    echo "    tasks: ${SMOKE_TASKS}"
    echo "    tools: $(IFS=,; echo "${tools[*]}")"
    echo ""

    start_time=$(date +%s)
    n=0
    for tool in "${tools[@]}"; do
      for repo in "${repos[@]}"; do
        for task in "${tasks[@]}"; do
          n=$((n + 1))
          echo "[$n/$total]"
          run_single "$repo" "$task" "$tool"
        done
      done
    done
    end_time=$(date +%s)

    print_summary

    echo ""
    echo "Total wall time: $(( end_time - start_time ))s"
    ;;

  single|"")
    [[ "$MODE" == "single" ]] && shift
    repo="${1:-gin}"
    task="${2:-conventions}"
    tool="${3:-sense}"
    echo "=== quick bench: $tool × $repo / $task ==="
    echo ""
    start_time=$(date +%s)
    run_single "$repo" "$task" "$tool"
    end_time=$(date +%s)
    echo "Wall time: $(( end_time - start_time ))s"
    ;;

  -h|--help)
    usage
    ;;

  *)
    echo "Unknown mode: $MODE" >&2
    usage
    exit 1
    ;;
esac
