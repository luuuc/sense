#!/usr/bin/env bash
# bench-paths.sh — resolve RESULTS_DIR and SCENARIOS_DIR for the active bench.
#
# A "bench" is either the GLOBAL bench (baseline + competitors vs sense, across
# all language/framework repos) or a VERTICAL bench (baseline vs sense only, one
# language/framework's repos). Select a vertical with VERTICAL=<name> (e.g.
# ruby-rails); empty/unset means the global bench.
#
# A vertical bench is also MODEL-SCOPED: set BENCH_MODEL=<id> and each model gets
# its own bench root, so sweeping several models (opus-4-8, gpt-5.5, an
# ollama-cloud model, ...) never overwrites another's results. The id is
# sanitized (/ and : -> _) to a safe dir name. The
# global bench is deliberately single-model: BENCH_MODEL is ignored there.
#
#   GLOBAL :           results/                              scenarios/
#   VERTICAL:          verticals/<name>/results/             verticals/<name>/scenarios/
#   VERTICAL + model:  verticals/<name>/results/<model>/     verticals/<name>/scenarios/
#
# Each vertical is one self-contained home: verticals/<name>/ holds repos.txt,
# scenarios/, and results/ together. The global bench keeps the legacy top-level
# results/ + scenarios/ roots (baseline + competitors vs sense).
#
# Source this AFTER setting BENCH_DIR (and optionally VERTICAL / BENCH_MODEL). It
# exports RESULTS_DIR and SCENARIOS_DIR so child scripts (run/score/judge/report)
# inherit the right roots. A pre-set RESULTS_DIR/SCENARIOS_DIR always wins, so an
# explicit override (or a parent that already resolved them) is never clobbered;
# multi-model loops `unset RESULTS_DIR` before re-sourcing to re-derive per model.
: "${BENCH_DIR:?bench-paths.sh: BENCH_DIR must be set before sourcing}"
if [ -n "${VERTICAL:-}" ]; then
  _results_base="$BENCH_DIR/verticals/$VERTICAL/results"
  SCENARIOS_DIR="${SCENARIOS_DIR:-$BENCH_DIR/verticals/$VERTICAL/scenarios}"
else
  _results_base="$BENCH_DIR/results"
  SCENARIOS_DIR="${SCENARIOS_DIR:-$BENCH_DIR/scenarios}"
fi
if [ -n "${VERTICAL:-}" ] && [ -n "${BENCH_MODEL:-}" ]; then
  _msan="$(printf '%s' "$BENCH_MODEL" | tr '/:' '__')"
  RESULTS_DIR="${RESULTS_DIR:-$_results_base/$_msan}"
else
  RESULTS_DIR="${RESULTS_DIR:-$_results_base}"
fi
export RESULTS_DIR SCENARIOS_DIR VERTICAL BENCH_MODEL
