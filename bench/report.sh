#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="$BENCH_DIR/results"
LIB_DIR="$BENCH_DIR/lib"

# --- Argument parsing ---

FORMAT="terminal"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --format) FORMAT="$2"; shift 2 ;;
    --json)   FORMAT="json"; shift ;;
    --md)     FORMAT="markdown"; shift ;;
    -h|--help)
      echo "Usage: report.sh [--format terminal|markdown|json] [--json] [--md]"
      echo ""
      echo "Generates comparison report from scored results."
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

# --- Generate report ---

output=$(python3 "$LIB_DIR/reporter.py" "$RESULTS_DIR" --format "$FORMAT")
echo "$output"

if [[ "$FORMAT" == "markdown" ]]; then
  echo "$output" > "$RESULTS_DIR/report.md"
  echo "Written to $RESULTS_DIR/report.md" >&2
elif [[ "$FORMAT" == "json" ]]; then
  echo "$output" > "$RESULTS_DIR/report.json"
  echo "Written to $RESULTS_DIR/report.json" >&2
fi
