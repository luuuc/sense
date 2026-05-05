#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"

REPO="${1:-gin}"
TASK="${2:-conventions}"
TOOL="${3:-sense}"

echo "=== quick bench: $TOOL × $REPO / $TASK ==="
echo ""

start_time=$(date +%s)

# Run the single combination
bash "$BENCH_DIR/run.sh" --tool "$TOOL" --repo "$REPO" --task "$TASK" --budget 1.00 --timeout 120

# Score it
bash "$BENCH_DIR/score.sh" --tool "$TOOL" --repo "$REPO" --task "$TASK"

end_time=$(date +%s)
wall=$((end_time - start_time))

# Print the score
result_dir="$BENCH_DIR/results/$TOOL/$REPO/$TASK"
if [[ -f "$result_dir/scored.json" ]]; then
  echo ""
  echo "=== RESULT ($TOOL × $REPO / $TASK) — ${wall}s ==="
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
    if 'misses' in metrics: parts.append(f\"misses={metrics['misses']}\")
    print(f'  Metrics: {\" | \".join(parts)}')
keywords = scored.get('matched_keywords', [])
missing = scored.get('missing_keywords', [])
if keywords: print(f'  Matched: {keywords}')
if missing: print(f'  Missing: {missing}')
" "$result_dir/scored.json"
else
  echo ""
  echo "=== NO SCORE (transcript or scoring failed) — ${wall}s ==="
  [[ -f "$result_dir/score.log" ]] && cat "$result_dir/score.log"
fi
