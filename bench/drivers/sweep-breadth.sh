#!/usr/bin/env bash
# sweep-breadth.sh — BREADTH-FIRST rails-vertical sweep for a metered arm
# (codex gpt / opencode), built to (a) land a COMPLETE board fast and (b) spend
# the fewest weekly-cap tokens to get it. The depth-first sweepers (sweep /
# sweep-resume at RUNS=2) finish one repo's ×2 before starting the next — slowest
# path to a full board and double the tokens up front. This walks breadth-first:
#
#   PASS 1  run-1 for EVERY repo  -> a full board at ~half the ×2 token cost.
#   PASS 2  add run-2 ONLY on the close calls (repos whose run-1 has NO group
#           clearing the +0.50 win bar, i.e. pergroup.py prints no "VERDICT: WIN")
#           -> the second run's tokens go only where they can still move the
#           verdict; confirmed wins stay at ×1.
#
# Every repo gets at least one run — Pass 1 covers ALL of them, big repos are NOT
# dropped (default smallest-first, so under a cap the most cells land before the
# big repos at the tail; resume next window picks up the rest).
#
# Harness-agnostic: dispatches through runs-variance.sh, so it serves any metered
# arm — codex (gpt-*/o3*/o4*/codex:*) or opencode (kimi-for-coding/*, *:cloud,
# ollama-cloud/*). The breadth logic and the close-call gate are identical; only
# the big-repo runaway guard differs by harness (below).
#
# BIG-REPO RUNAWAY GUARD: big repos get a bounded session length so one stuck run
# can't drain the weekly budget. This is a runaway guard ONLY — it does NOT touch
# the scorer's TIME_CEILINGS, so efficiency scoring is unchanged. Per harness:
#   - codex:    BENCH_CODEX_TIMEOUT (wall kill, default 540s).
#   - opencode: OPENCODE_MAX_SECS (hard cap, default 1800s) AND a RAISED
#               OPENCODE_STALL_IDLE (default 600s on big repos, 300s elsewhere) —
#               big-repo grep baselines idle for minutes, so the idle watchdog is
#               *loosened*, while MAX_SECS bounds a true hang.
# Trade-off: too tight truncates the heavier sense arm into a false loss, so keep
# the ceiling above observed productive completion (opus finishes big repos in
# ~300s; the metered arms run longer).
#
# Resumable + cap-aware: a repo already valid for this model is skipped, and a
# session failure (codex_session_failed / opencode_session_failed / a watchdog
# stall) stops the pass cleanly — re-run to continue. NOTE: Kimi has no clean cap
# error, so on a flat subscription rely on the pacing cooldown + your budget read,
# not only on this auto-stop.
#
#   MODELS="gpt-5.5" bash bench/drivers/sweep-breadth.sh                    # codex, both passes
#   MODELS="kimi-for-coding/k2p7" bash bench/drivers/sweep-breadth.sh       # opencode/Kimi, both passes
#   MODELS="gpt-5.5" PASS=1 bash bench/drivers/sweep-breadth.sh             # run-1 board only
#   MODELS="gpt-5.5" PASS=2 bash bench/drivers/sweep-breadth.sh             # hardening only
#   MODELS="kimi-for-coding/k2p7" CLOSE="mastodon discourse" PASS=2 ...   # force the close set
#   MODELS="gpt-5.5" SKIP_BIG=1 bash bench/drivers/sweep-breadth.sh         # defer gitlabhq/rails (cap reality)
#   SKIP_ENSURE_INDEX=1 MODELS="kimi-for-coding/k2p7" bash bench/...      # env passes through to runs-variance
#
set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"; cd "$BENCH_DIR/.."
VERTICAL="${VERTICAL-ruby-rails}"; source "$BENCH_DIR/lib/bench-paths.sh"

MODEL="${MODELS:?set MODELS to ONE model id, e.g. MODELS=gpt-5.5}"
JUDGE="${BENCH_JUDGE_MODEL:-claude-sonnet-4-6}"; export BENCH_JUDGE_MODEL="$JUDGE"
PASS="${PASS:-both}"                # 1 | 2 | both
WINBAR="${WINBAR:-0.50}"            # close-call threshold (mirrors pergroup VERDICT)
SKIP_BIG="${SKIP_BIG:-0}"          # defer the cost-outlier repos (only if the cap forces it)

# Smallest-first by indexed-symbol count (bench/index-state.json) so that under a
# weekly cap the most cells land before the big repos at the tail. Overridable.
REPOS="${REPOS:-raix langchainrb lobsters ruby_llm llm.rb solidus redmine chatwoot forem mastodon rails discourse gitlabhq}"
# Cost outliers held back by SKIP_BIG=1 (178k + the framework): only when a cap forces it.
HUGE="${HUGE_REPOS:-gitlabhq rails}"
# Repos that get the runaway guard (mid-big; the huge ones keep their harness
# default — they legitimately run long and tightening them just truncates).
BIG_REPOS="${BIG_REPOS:-discourse mastodon forem chatwoot redmine solidus}"
# codex wall kill (seconds) for BIG_REPOS:
BIG_TIMEOUT="${BIG_TIMEOUT:-540}"
# opencode session bounds: hard cap + idle watchdog (RAISED on big repos so a slow
# grep baseline isn't killed idle; the rest get the tighter idle below).
OPENCODE_BIG_MAX_SECS="${OPENCODE_BIG_MAX_SECS:-1800}"
OPENCODE_BIG_STALL_IDLE="${OPENCODE_BIG_STALL_IDLE:-600}"
OPENCODE_STALL_IDLE="${OPENCODE_STALL_IDLE:-300}"

case "$MODEL" in
  gpt-*|o3*|o4*|codex:*) HARNESS=codex ;;
  claude-*)              HARNESS=claude ;;   # no metered guard needed
  *)                     HARNESS=opencode ;; # kimi-for-coding/*, *:cloud, ollama-cloud/*
esac

modelroot="$RESULTS_DIR/$(echo "$MODEL" | tr '/:' '__')"
echo "[breadth] model=$MODEL harness=$HARNESS judge=$JUDGE pass=$PASS root=$modelroot skip_big=$SKIP_BIG"
echo "[breadth] order: $REPOS"

# ---- helpers --------------------------------------------------------------
ERR_FLAGS='codex_session_failed\|provider_cap_error\|empty_final_answer\|opencode_session_failed\|stalled_midrun\|hard_cap_timeout'

has_run1()  { [ -f "$modelroot/sense/$1/run-1/transcript.json" ]; }

errored() {  # $1=repo — any error flag on EITHER arm's run_meta
  grep -lq "$ERR_FLAGS" \
    "$modelroot/sense/$1"/run-*/run_meta.json \
    "$modelroot/baseline/$1"/run-*/run_meta.json 2>/dev/null
}

adopted() {  # $1=repo — sense arm actually reached Sense (codex false-tie gate)
  local ch; for ch in "$modelroot/sense/$1"/run-*/channels.json; do
    [ -f "$ch" ] || continue
    python3 -c "import json,sys;c=json.load(open('$ch'))['channels'];sys.exit(0 if c.get('mcp_sense',0)+c.get('cli_sense',0)>0 else 1)" 2>/dev/null && return 0
  done
  # No channels.json (non-codex harness) -> adoption is checked elsewhere; don't block.
  ls "$modelroot/sense/$1"/run-*/channels.json >/dev/null 2>&1 || return 0
  return 1
}

is_valid() { has_run1 "$1" && ! errored "$1" && adopted "$1"; }

is_close() {  # $1=repo — run-1 has NO group clearing the win bar (worth a 2nd run)
  ! RESULTS_DIR="$modelroot" python3 bench/lib/pergroup.py "$1" "$WINBAR" 2>/dev/null \
      | grep -q "VERDICT: WIN"
}

deferred() { [ "$SKIP_BIG" = 1 ] && [[ " $HUGE " == *" $1 "* ]]; }

# Per-harness runaway guard for repo $1 -> echoes a space-separated list of
# KEY=VALUE env assignments (empty = harness default). codex gets a wall kill;
# opencode gets a hard cap + an idle watchdog (raised on big repos).
guard_env_for() {
  local repo="$1" big=0
  [[ " $BIG_REPOS " == *" $repo "* ]] && big=1
  case "$HARNESS" in
    codex)    [ "$big" = 1 ] && echo "BENCH_CODEX_TIMEOUT=$BIG_TIMEOUT" ;;
    opencode) if [ "$big" = 1 ]; then echo "OPENCODE_MAX_SECS=$OPENCODE_BIG_MAX_SECS OPENCODE_STALL_IDLE=$OPENCODE_BIG_STALL_IDLE"
              else echo "OPENCODE_STALL_IDLE=$OPENCODE_STALL_IDLE"; fi ;;
  esac
}

bench_one() {  # $1=repo  $2=start_run  $3=keep_runs
  local repo="$1" start="$2" keep="$3"
  local guard=(); read -ra guard <<< "$(guard_env_for "$repo")"
  echo "▶ $repo  (run $start, guard=${guard[*]:-default})"
  # ${arr[@]+"${arr[@]}"} expands to nothing when the array is empty — bash 3.2
  # (macOS default) errors on a bare "${arr[@]}" under `set -u` otherwise.
  env ${guard[@]+"${guard[@]}"} MODELS="$MODEL" RUNS=1 START_RUN="$start" KEEP_RUNS="$keep" \
    bash bench/drivers/runs-variance.sh "$repo" || true
}

# ---- PASS 1: run-1 for every repo (full board) ----------------------------
pass1() {
  echo "=== PASS 1 — run-1 across all repos ==="
  for repo in $REPOS; do
    deferred "$repo" && { echo "⏭  defer $repo (SKIP_BIG=1)"; continue; }
    if is_valid "$repo"; then echo "✓ skip $repo (run-1 valid)"; continue; fi
    bench_one "$repo" 1 0
    if errored "$repo"; then
      echo "⛔ cap/failure at $repo — stopping Pass 1 cleanly. Re-run after the reset"
      echo "   to resume; valid repos are skipped."
      return 0
    fi
  done
  echo "✅ PASS 1 complete — full run-1 board for $MODEL"
}

# ---- PASS 2: add run-2 ONLY on the close calls ----------------------------
pass2() {
  echo "=== PASS 2 — run-2 on close calls only (bar +${WINBAR}) ==="
  local set2="${CLOSE:-}"
  if [ -z "$set2" ]; then
    for repo in $REPOS; do
      deferred "$repo" && continue
      is_valid "$repo" || { echo "·  $repo: no valid run-1 — skip (Pass 1 first)"; continue; }
      [ -d "$modelroot/sense/$repo/run-2" ] && { echo "✓ $repo: run-2 exists"; continue; }
      if is_close "$repo"; then set2="$set2 $repo"; echo "→ $repo: CLOSE (run-1 < bar) — will harden"
      else echo "✓ $repo: confirmed win at run-1 — no run-2 (token save)"; fi
    done
  else
    echo "[breadth] CLOSE override: $set2"
  fi
  for repo in $set2; do
    bench_one "$repo" 2 1
    if errored "$repo"; then
      echo "⛔ cap/failure at $repo — stopping Pass 2 cleanly. Re-run after the reset."
      return 0
    fi
  done
  echo "✅ PASS 2 complete"
}

case "$PASS" in
  1)    pass1 ;;
  2)    pass2 ;;
  both) pass1; pass2 ;;
  *)    echo "PASS must be 1|2|both" >&2; exit 1 ;;
esac

bash bench/drivers/report-matrix.sh >/dev/null 2>&1 || echo "[warn] matrix refresh failed" >&2
echo "[breadth] done. Read: RESULTS_DIR=$modelroot python3 bench/lib/pergroup.py <repo>; bash bench/drivers/report-matrix.sh"
