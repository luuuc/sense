#!/usr/bin/env bash
# bench.sh — run + score + judge + report in sequence.
#
# Forwards --tool / --repo filters to run.sh, score.sh, judge.sh.
# report.sh always renders the full results/ tree (no filter), and
# is run twice to emit both results/report.md and results/report.json.

set -euo pipefail

BENCH2_DIR="$(cd "$(dirname "$0")" && pwd)"

bash "$BENCH2_DIR/run.sh"    "$@"
bash "$BENCH2_DIR/score.sh"  "$@"
bash "$BENCH2_DIR/judge.sh"  "$@"
bash "$BENCH2_DIR/report.sh" --md
bash "$BENCH2_DIR/report.sh" --json
