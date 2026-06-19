#!/usr/bin/env bash
# sweep-resume.sh — walk the rails-vertical repos in BIGGEST-OPUS-WIN-FIRST order,
# resumable + cap-aware, so a metered LLM arm (opencode ollama-cloud / codex gpt)
# spends its window on the most DISCRIMINATING repos and continues next session
# with NO loss when a usage cap trips.
#
# Why win-order (not smallest-first): the cheap/LLM arms hit usage caps mid-sweep.
# Smallest-first burns the cap on small-readable repos where even Sense ties; the
# Opus headline shows the win concentrates on the big scattered-fan-out repos. So
# we run the biggest-Opus-win repos FIRST — a capped session still produces the
# rows that matter for the LLM-vs-Opus comparison.
#
# Resume: a repo already benched VALID for this model is SKIPPED, so re-running the
# script after a cap reset continues where it stopped.
# Cap pause: if a run comes back with a provider cap error, the sweep STOPS cleanly
# (re-run next session to resume). SKIP_BIG=1 defers the huge repos (gitlabhq/rails)
# so a tight window is not blown on one 177k-symbol repo.
#
#   MODELS="ollama-cloud/qwen3-coder:480b" RUNS=2 bash bench/sweep-resume.sh
#   MODELS="codex/gpt-5.5" RUNS=2 SKIP_BIG=1 bash bench/sweep-resume.sh
#
set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"; cd "$BENCH_DIR/.."
VERTICAL="${VERTICAL-ruby-rails}"; source "$BENCH_DIR/lib/bench-paths.sh"
MODEL="${MODELS:?set MODELS to ONE model id}"; RUNS="${RUNS:-2}"
SKIP_BIG="${SKIP_BIG:-0}"

# Biggest Opus cited-recall win first → lowest. Ties (lobsters/raix) + the re-aimed
# redmine sit last (least likely to separate on an LLM). Update if the Opus board moves.
WINORDER=(mastodon gitlabhq chatwoot discourse solidus forem ruby_llm rails llm.rb langchainrb redmine lobsters raix)
# The cost outliers (run last / behind SKIP_BIG): 177k+ and the framework.
BIG="gitlabhq rails"

modelroot="$RESULTS_DIR/$(echo "$MODEL" | tr '/:' '__')"
echo "sweep-resume: model=$MODEL runs=$RUNS root=$modelroot skip_big=$SKIP_BIG"

is_valid() {  # $1=repo — a sense run-1 transcript exists with no cap/empty error
  local rd="$modelroot/sense/$1"
  [ -f "$rd/run-1/transcript.json" ] || return 1
  ! grep -lq "provider_cap_error\|empty_final_answer\|opencode_session_failed" \
      "$rd"/run-*/run_meta.json 2>/dev/null
}
capped() {  # $1=repo — last run flagged a provider cap error
  grep -lq "provider_cap_error" "$modelroot/sense/$1"/run-*/run_meta.json 2>/dev/null \
    || grep -lq "provider_cap_error" "$modelroot/baseline/$1"/run-*/run_meta.json 2>/dev/null
}

for repo in "${WINORDER[@]}"; do
  if [[ "$SKIP_BIG" == "1" && " $BIG " == *" $repo "* ]]; then
    echo "⏭  defer $repo (SKIP_BIG=1, cost outlier)"; continue
  fi
  if is_valid "$repo"; then echo "✓ skip $repo (already benched valid)"; continue; fi
  echo "▶ benching $repo ..."
  MODELS="$MODEL" RUNS="$RUNS" bash bench/runs-variance.sh "$repo" || true
  if capped "$repo"; then
    echo "⛔ usage cap at $repo — stopping cleanly. Re-run this script after the cap"
    echo "   resets; benched repos are skipped, so it resumes at the next one."
    exit 0
  fi
done
echo "✅ sweep complete for $MODEL (all win-ordered repos benched)."
echo "   Read: bash bench/report-matrix.sh"
