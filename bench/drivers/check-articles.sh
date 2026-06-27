#!/usr/bin/env bash
# check-articles.sh — the article gate for the vertical article set.
#
# Runs both article checks and fails if either fails:
#   1. headline freshness  (lib/check_article_stats.py) — recomputes each
#      teardown's claimed headline numbers from its `data:` bench root and
#      prints FRESH / OUTDATED. Args ($@, e.g. --tol 0.02) pass through here.
#   2. structural + referential audit (lib/article_audit.py) — coverage (every
#      benched repo has one pack), Block A-J structure, frontmatter keys, broken
#      local links, README board sync, _skeleton.md's embedded board vs the
#      canonical scoreboard, and an em-dash WARN. FAILs fail the gate.
#
# Run after any re-bench / re-judge / re-scan so a draft never ships stale or
# structurally-broken. Numbers + structure only; the prose stays the author's.
#
#   bash bench/drivers/check-articles.sh
#   bash bench/drivers/check-articles.sh <articles_dir> --tol 0.02
set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
rc=0

echo "== headline freshness =="
python3 "$BENCH_DIR/lib/check_article_stats.py" "$@" || rc=$?

echo
echo "== structural + referential audit =="
python3 "$BENCH_DIR/lib/article_audit.py" || rc=$?

exit $rc
