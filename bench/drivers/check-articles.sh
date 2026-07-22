#!/usr/bin/env bash
# check-articles.sh - the article gate for the vertical article set.
#
# Runs both article checks and fails if either fails:
#   1. headline freshness  (lib/check_article_stats.py) - recomputes each
#      teardown's claimed headline numbers from its `data:` bench root and
#      prints FRESH / OUTDATED.
#   2. structural + referential audit (lib/article_audit.py) - coverage (every
#      benched repo has one pack), Block A-J structure, frontmatter keys, broken
#      local links, README board sync, _skeleton.md's embedded board vs the
#      canonical scoreboard, and an em-dash WARN. FAILs fail the gate.
#
# Run after any re-bench / re-judge / re-scan so a draft never ships stale or
# structurally-broken. Numbers + structure only; the prose stays the author's.
#
#   bash bench/drivers/check-articles.sh
#   bash bench/drivers/check-articles.sh <articles_dir> [--results <root>] [--tol 0.02]
#
# ⚠️ BOTH checks must be pointed at the SAME article set. Previously the
# audit was invoked with NO arguments, so it always ran against its own
# hardcoded default (the rails vertical) while the freshness check ran against
# whatever you passed. `check-articles.sh <go-articles>` therefore printed a
# green structural audit OF A DIFFERENT VERTICAL, and the go set was recorded as
# having passed a gate it was never run through. An unpointed gate is worse than
# no gate: it reports success for work it never inspected.
set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
rc=0

ARTICLES=""
RESULTS=""
PASSTHRU=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --results) RESULTS="$2"; shift 2 ;;
    --tol)     PASSTHRU+=(--tol "$2"); shift 2 ;;
    -h|--help)
      echo "Usage: check-articles.sh [<articles_dir>] [--results <root>] [--tol N]"
      exit 0 ;;
    *) ARTICLES="$1"; shift ;;
  esac
done

stats_args=()
audit_args=()
[[ -n "$ARTICLES" ]] && { stats_args+=("$ARTICLES"); audit_args+=("$ARTICLES"); }
[[ -n "$RESULTS"  ]] && audit_args+=(--results "$RESULTS")

echo "== headline freshness =="
echo "   articles: ${ARTICLES:-<lib default>}"
python3 "$BENCH_DIR/lib/check_article_stats.py" ${stats_args[@]+"${stats_args[@]}"} ${PASSTHRU[@]+"${PASSTHRU[@]}"} || rc=$?

echo
echo "== structural + referential audit =="
echo "   articles: ${ARTICLES:-<lib default>}  results: ${RESULTS:-<lib default>}"
python3 "$BENCH_DIR/lib/article_audit.py" ${audit_args[@]+"${audit_args[@]}"} || rc=$?

exit $rc
