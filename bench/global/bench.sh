#!/usr/bin/env bash
# bench.sh — run + score + judge + report in sequence.
#
# Parses args once and routes them to the step that actually accepts them:
#   --tool/--repo                          → run, score, judge (filters)
#   --dry-run/--budget/--timeout/--runs    → run only
#   --model/--tag/--build                  → run only
#   --force                                → judge only
# report.sh always renders the full results/ tree (no filter), and is run
# twice to emit both results/report.md and results/report.json.

set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"

RUN_ARGS=()
SCORE_ARGS=()
JUDGE_ARGS=()

usage() {
  cat <<'EOF'
Usage: bench.sh [--tool t1,t2] [--repo r1,r2] [--runs N] [--model MODEL] \
                [--dry-run] [--budget USD] [--timeout SECS] \
                [--tag TAG] [--build] [--force]

Runs the full pipeline: run.sh → score.sh → judge.sh → report.sh (md+json).

Filters (--tool, --repo) apply to all three working steps. Run-only flags
(--dry-run, --budget, --timeout, --runs, --model, --tag, --build) are
forwarded only to run.sh. --force is forwarded only to judge.sh.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool|--repo)
      RUN_ARGS+=("$1" "$2")
      SCORE_ARGS+=("$1" "$2")
      JUDGE_ARGS+=("$1" "$2")
      shift 2 ;;
    --dry-run|--build)
      RUN_ARGS+=("$1"); shift ;;
    --budget|--timeout|--runs|--model|--tag)
      RUN_ARGS+=("$1" "$2"); shift 2 ;;
    --force)
      JUDGE_ARGS+=("$1"); shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage; exit 1 ;;
  esac
done

bash "$BENCH_DIR/global/run.sh"   "${RUN_ARGS[@]}"
bash "$BENCH_DIR/score.sh" "${SCORE_ARGS[@]}"
bash "$BENCH_DIR/judge.sh" "${JUDGE_ARGS[@]}"
bash "$BENCH_DIR/report.sh" --md
bash "$BENCH_DIR/report.sh" --json
