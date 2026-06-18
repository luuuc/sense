#!/usr/bin/env bash
# check-articles.sh — freshness gate for the vertical article drafts.
#
# Recomputes each teardown's headline numbers from its `data:` bench model root
# and compares them to the `headline:` block the article claims, printing
# FRESH / OUTDATED per article. Run it after any re-bench so a draft never
# ships stale figures. Numbers only; the prose stays the author's.
#
#   bash bench/check-articles.sh
#   bash bench/check-articles.sh <articles_dir> --tol 0.02
set -euo pipefail
BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
python3 "$BENCH_DIR/lib/check_article_stats.py" "$@"
