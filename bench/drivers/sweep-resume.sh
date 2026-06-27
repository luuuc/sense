#!/usr/bin/env bash
# sweep-resume.sh — walk the rails-vertical repos in BIGGEST↔SMALLEST INTERLEAVE
# order (biggest, smallest, 2nd-biggest, 2nd-smallest, …), resumable + cap-aware,
# so any arm — the Opus headline or a metered LLM arm (opencode ollama-cloud /
# codex gpt) — hits the most bench-revealing repos FIRST and continues next
# session with NO loss when a usage cap trips.
#
# Why interleave (not smallest-first, not pure-biggest-first): the big repos
# (discourse, rails, forem) are both where Sense's reach win concentrates AND
# where any harness/scoring regression surfaces — so test them early, while you
# can still tweak the bench or Sense, instead of burning the window on small
# repos that produce no concrete result. The interleaved small repo after each
# big one is near-free and confirms the score/judge pipeline still works end to
# end before the next big spend.
#
# EXCEPTION — gitlabhq runs LAST (not first), even though it is the biggest: a
# rescan of its 177k-symbol index can take HOURS, and we don't want that to block
# the whole sweep at the very start. So discourse (the biggest repo that rescans
# quickly) anchors the front; gitlabhq is appended last so every other result
# lands before the sweep risks a multi-hour gitlabhq rebuild/bench.
#
# Resume: a repo already benched VALID for this model is SKIPPED, so re-running the
# script after a cap reset continues where it stopped.
# Cap pause: if a run comes back with a provider cap error, the sweep STOPS cleanly
# (re-run next session to resume). SKIP_BIG=1 defers the cost-outlier repos
# (gitlabhq/rails) for a metered arm whose window genuinely can't afford a 178k/52k
# embed-and-bench — note this overrides the test-huge-first intent, so only set it
# when the cap forces it (the Opus headline runs WITHOUT SKIP_BIG).
#
#   MODELS="ollama-cloud/qwen3-coder-next" RUNS=2 bash bench/drivers/sweep-resume.sh
#   MODELS="gpt-5.5" RUNS=2 SKIP_BIG=1 bash bench/drivers/sweep-resume.sh   # cap-tight metered arm
#
set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"; cd "$BENCH_DIR/.."
VERTICAL="${VERTICAL-ruby-rails}"; source "$BENCH_DIR/lib/bench-paths.sh"
MODEL="${MODELS:?set MODELS to ONE model id}"; RUNS="${RUNS:-2}"
SKIP_BIG="${SKIP_BIG:-0}"

# Biggest↔smallest interleave by indexed-symbol count (see bench/index-state.json),
# but with gitlabhq moved to the very END (its rescan can take hours — see the header).
# Interleaved 12 non-gitlab repos: discourse(59k) raix(177) rails(52k) langchainrb(1.0k)
# forem(18k) lobsters(1.8k) mastodon(18k) ruby_llm(2.0k) chatwoot(14k) llm.rb(2.2k)
# redmine(13k) solidus(9k); then gitlabhq(178k) last.
# Keep in sync with rescan-all.sh (which uses the same sizes, smallest-first, for indexing).
WINORDER=(discourse raix rails langchainrb forem mastodon ruby_llm chatwoot llm.rb redmine solidus lobsters gitlabhq)
# The cost outliers held back by SKIP_BIG=1 (only when a metered cap forces it): 178k + the framework.
BIG="gitlabhq rails"

modelroot="$RESULTS_DIR/$(echo "$MODEL" | tr '/:' '__')"
echo "sweep-resume: model=$MODEL runs=$RUNS root=$modelroot skip_big=$SKIP_BIG"

is_valid() {  # $1=repo — sense run-1 exists; no cap/empty/stall/hard-cap on EITHER arm
  local rd="$modelroot/sense/$1"
  [ -f "$rd/run-1/transcript.json" ] || return 1
  # Reject explicit error flags AND the watchdog kinds opencode-run.sh already records
  # (stalled_midrun=rc125, hard_cap_timeout=rc124). A throttle stall-kill keeps error=None
  # but its partial answer can clear the char gate, so it would otherwise look "valid" and
  # get skipped. Scan BOTH arms: a baseline stall contaminates the comparison too.
  ! grep -lq "provider_cap_error\|empty_final_answer\|opencode_session_failed\|stalled_midrun\|hard_cap_timeout" \
      "$modelroot/sense/$1"/run-*/run_meta.json "$modelroot/baseline/$1"/run-*/run_meta.json 2>/dev/null
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
  MODELS="$MODEL" RUNS="$RUNS" bash bench/drivers/runs-variance.sh "$repo" || true
  if capped "$repo"; then
    echo "⛔ usage cap at $repo — stopping cleanly. Re-run this script after the cap"
    echo "   resets; benched repos are skipped, so it resumes at the next one."
    exit 0
  fi
done
echo "✅ sweep complete for $MODEL (all win-ordered repos benched)."
echo "   Read: bash bench/drivers/report-matrix.sh"
