#!/usr/bin/env bash
# report-matrix.sh — render a vertical's CROSS-MODEL matrix (one row per model)
# into verticals/<name>/results/report.md and report.json.
#
# A vertical bench is model-scoped (verticals/<name>/results/<model>/<arm>/<repo>);
# the per-model reports come from report.sh, this is the model-comparison view on
# top. Defaults to the ruby-rails vertical; override with VERTICAL=<name>.
#
#   bash bench/drivers/report-matrix.sh
#   VERTICAL=ruby-rails bash bench/drivers/report-matrix.sh
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
VERTICAL="${VERTICAL-ruby-rails}"
[ -n "$VERTICAL" ] || { echo "report-matrix.sh: VERTICAL must be set" >&2; exit 1; }
# BENCH_MODEL unset here: RESULTS_DIR resolves to the vertical's base (the parent
# of the per-model roots), which is exactly what the matrix scans.
unset BENCH_MODEL
source "$BENCH_DIR/lib/bench-paths.sh"
LIB_DIR="$BENCH_DIR/lib"

python3 "$LIB_DIR/matrix.py" "$RESULTS_DIR" --format markdown > "$RESULTS_DIR/report.md"
python3 "$LIB_DIR/matrix.py" "$RESULTS_DIR" --format json     > "$RESULTS_DIR/report.json"
echo "Written $RESULTS_DIR/report.md and report.json" >&2
cat "$RESULTS_DIR/report.md"
