#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
# Resolves RESULTS_DIR (and SCENARIOS_DIR) for the global or VERTICAL bench.
source "$BENCH_DIR/lib/bench-paths.sh"
LIB_DIR="$BENCH_DIR/lib"

FORMAT="terminal"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --format) FORMAT="$2"; shift 2 ;;
    --json)   FORMAT="json"; shift ;;
    --md)     FORMAT="markdown"; shift ;;
    -h|--help)
      echo "Usage: report.sh [--format terminal|markdown|json] [--json] [--md]"
      echo ""
      echo "Generates comparison report from scored scenario results."
      echo ""
      echo "Outputs:"
      echo "  terminal (default)  — formatted table to stdout"
      echo "  markdown (--md)     — also writes results/report.md"
      echo "  json (--json)       — also writes results/report.json"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

# A vertical bench (VERTICAL set) gets the plain-English, internal-reference-free
# prose; the global bench (VERTICAL empty) keeps its existing report verbatim.
output=$(python3 "$LIB_DIR/reporter.py" "$RESULTS_DIR" --format "$FORMAT" ${VERTICAL:+--vertical})
echo "$output"

if [[ "$FORMAT" == "markdown" ]]; then
  echo "$output" > "$RESULTS_DIR/report.md"
  echo "Written to $RESULTS_DIR/report.md" >&2
elif [[ "$FORMAT" == "json" ]]; then
  echo "$output" > "$RESULTS_DIR/report.json"
  echo "Written to $RESULTS_DIR/report.json" >&2
fi
