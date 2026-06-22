#!/usr/bin/env bash
# throttle-pacing.sh — subscription-throttle pacing for the METERED cheap-model
# bench arms (opencode/Kimi, codex/GPT). NEVER used by the claude/opus path
# (bench-sense-local.sh does not source this file), so the ample-plan arm is
# untouched.
#
# Why this exists: the cheap arms run on small flat-rate coding subscriptions,
# token-metered on a ~5h rolling window. The bench fires sessions back-to-back
# (baseline then sense, per repo, across many repos), so mid-sweep the window
# drains and the SENSE arm — roughly 2x the token weight (large Sense JSON +
# file reads) — gets its stream truncated. A truncated run still clears the
# 200-char answer gate, so it is scored as real and manufactures a FALSE LOSS
# (e.g. discourse sense cited_recall 0.15 mid-sweep vs 0.83 uncontended). Pacing
# spaces the sessions so each runs in a fresh-enough window.
#
# MASTER SWITCH:
#   BENCH_THROTTLE_PACING   (default 1/on). Set 0 for a clean no-op revert to
#                           exactly today's behavior: back-to-back sessions,
#                           immediate retry, no lock, no health log. Every helper
#                           below early-returns when off, so a 0 is byte-for-byte
#                           the pre-pacing flow.
#
# KNOBS (active only when the master switch is on):
#   OPENCODE_PACE_SECONDS         (90)   inter-session delay between arms, runs, repos
#   BENCH_BACKOFF_BASE_SECONDS    (120)  first backoff before a throttle/truncation retry
#   BENCH_BACKOFF_MAX_SECONDS     (600)  cap on the exponential backoff
#   BENCH_WINDOW_COOLDOWN_SECONDS (1800) long pause after N consecutive degraded sessions
#   BENCH_DEGRADED_STREAK_LIMIT   (2)    consecutive degraded sessions that trigger cooldown
#   BENCH_SENSE_FIRST             (0)    run the heavier sense arm first, into the fresher window
#   BENCH_HEALTH_LOG              (results/.../throttle-health.log) one line per session
#   BENCH_PACE_STATE             (/tmp/sense-bench-pace.streak) consecutive-degraded counter file
#
# Source this AFTER bench-paths.sh (so RESULTS_DIR is resolvable for the default
# health-log path). Pacing is correct only relative to a given plan, hence the
# master toggle.

BENCH_THROTTLE_PACING="${BENCH_THROTTLE_PACING:-1}"
OPENCODE_PACE_SECONDS="${OPENCODE_PACE_SECONDS:-90}"
BENCH_BACKOFF_BASE_SECONDS="${BENCH_BACKOFF_BASE_SECONDS:-120}"
BENCH_BACKOFF_MAX_SECONDS="${BENCH_BACKOFF_MAX_SECONDS:-600}"
BENCH_WINDOW_COOLDOWN_SECONDS="${BENCH_WINDOW_COOLDOWN_SECONDS:-1800}"
BENCH_DEGRADED_STREAK_LIMIT="${BENCH_DEGRADED_STREAK_LIMIT:-2}"
BENCH_SENSE_FIRST="${BENCH_SENSE_FIRST:-0}"
BENCH_PACE_STATE="${BENCH_PACE_STATE:-${TMPDIR:-/tmp}/sense-bench-pace.streak}"

# pacing_on — the single gate every helper checks first.
pacing_on() { [ "${BENCH_THROTTLE_PACING:-1}" = 1 ]; }

# pace_sleep <seconds> <why> — inter-session spacing. No-op when off or seconds<=0.
pace_sleep() {
  pacing_on || return 0
  local secs="${1:-0}" why="${2:-pacing}"
  [ "$secs" -gt 0 ] 2>/dev/null || return 0
  echo "[pace] sleeping ${secs}s — $why" >&2
  sleep "$secs"
}

# pace_backoff <attempt> <why> — LONG exponential backoff before a throttle/
# truncation retry, so the next attempt lands in a fresher window. attempt is
# 1-based: sleep = min(base * 2^(attempt-1), max). Use this ONLY for throttle/
# cap/truncation; a true no-output hang is not throttle-related — retry it fast
# (caller skips this).
pace_backoff() {
  pacing_on || return 0
  local attempt="${1:-1}" why="${2:-throttle}" secs="$BENCH_BACKOFF_BASE_SECONDS" i=1
  while [ "$i" -lt "$attempt" ]; do secs=$(( secs * 2 )); i=$(( i + 1 )); done
  [ "$secs" -gt "$BENCH_BACKOFF_MAX_SECONDS" ] && secs="$BENCH_BACKOFF_MAX_SECONDS"
  echo "[pace] backoff ${secs}s before retry (attempt $attempt) — $why" >&2
  sleep "$secs"
}

# Strict serialization: a PER-SUBSCRIPTION mkdir-lock so two sessions on the SAME
# metered plan can never overlap (the concurrency disaster). Different providers
# (kimi vs gpt) take different lock names and stay independent, as do opus sweeps
# (which never source this file). A stale lock from a dead pid is auto-stolen so a
# killed session cannot deadlock the next one.
#   pace_lock_acquire <name>   — block until the named lock is held
#   pace_lock_release          — drop it (also wired to EXIT by acquire)
pace_lock_acquire() {
  pacing_on || return 0
  local name="${1:-session}"
  local lock="${TMPDIR:-/tmp}/sense-bench-${name}.lock" holder
  while ! mkdir "$lock" 2>/dev/null; do
    holder="$(cat "$lock/pid" 2>/dev/null || echo '')"
    if [ -n "$holder" ] && ! kill -0 "$holder" 2>/dev/null; then
      echo "[pace] stealing stale $name lock from dead pid $holder" >&2
      rm -rf "$lock"; continue
    fi
    echo "[pace] another $name bench session is running (pid ${holder:-?}); waiting 5s..." >&2
    sleep 5
  done
  echo "$$" > "$lock/pid"
  BENCH_PACE_LOCK_HELD="$lock"
  trap 'pace_lock_release' EXIT
}
pace_lock_release() {
  [ -n "${BENCH_PACE_LOCK_HELD:-}" ] || return 0
  rmdir "$BENCH_PACE_LOCK_HELD" 2>/dev/null || rm -rf "$BENCH_PACE_LOCK_HELD" 2>/dev/null
  BENCH_PACE_LOCK_HELD=""
}

# pace_order_tools <arms...> — echo the arms one per line in run order. With
# BENCH_SENSE_FIRST=1 the heavier sense arm goes first (into the fresher window);
# otherwise the input order is preserved. 3.2-safe (no mapfile).
pace_order_tools() {
  if pacing_on && [ "${BENCH_SENSE_FIRST:-0}" = 1 ]; then
    local a rest='' had_sense=0
    for a in "$@"; do
      if [ "$a" = sense ]; then had_sense=1; else rest="$rest $a"; fi
    done
    [ "$had_sense" = 1 ] && echo sense
    for a in $rest; do echo "$a"; done
  else
    local a; for a in "$@"; do echo "$a"; done
  fi
}

# pace_health_log <repo> <arm> <wall> <otok> <achars> <attempts> <class> — one
# legible line per session so throttle ONSET is observable live, not reconstructed
# after the sweep. Goes to stderr and (when resolvable) appends to BENCH_HEALTH_LOG.
pace_health_log() {
  pacing_on || return 0
  local line="[health] repo=$1 arm=$2 wall=${3}s otok=$4 achars=$5 attempts=$6 class=$7"
  echo "$line" >&2
  local f="${BENCH_HEALTH_LOG:-${RESULTS_DIR:-${TMPDIR:-/tmp}}/throttle-health.log}"
  { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$line" >> "$f"; } 2>/dev/null || true
}

# --- consecutive-degraded-session cooldown (lever 3) ---------------------------
# The streak persists on disk so it survives across runner invocations (each
# `runs-variance` run is a separate process). After BENCH_DEGRADED_STREAK_LIMIT
# consecutive degraded sessions, pause a full window-reset cooldown, then reset.
pace_streak_get() { cat "$BENCH_PACE_STATE" 2>/dev/null || echo 0; }
pace_streak_reset() { echo 0 > "$BENCH_PACE_STATE" 2>/dev/null || true; }

# pace_session_classify <repo> <results_dir> <run_k> — echo "degraded" or "clean"
# by inspecting BOTH arms' run_meta for this run. Degraded = a real throttle
# artifact: a cap/empty/session error flagged by the runner's own classification.
pace_session_classify() {
  local repo="$1" rdir="$2" k="$3" arm meta
  for arm in baseline sense; do
    meta="$rdir/$arm/$repo/run-$k/run_meta.json"
    [ -f "$meta" ] || meta="$rdir/$arm/$repo/run_meta.json"
    [ -f "$meta" ] || continue
    if grep -Eq '"error"[[:space:]]*:[[:space:]]*"(empty_final_answer|provider_cap_error|opencode_session_failed|codex_session_failed)"' "$meta"; then
      echo degraded; return 0
    fi
  done
  echo clean
}

# pace_note_session <degraded|clean> — advance/reset the streak; cool down when it
# hits the limit. Loud logging so the onset and the cooldown are visible live.
pace_note_session() {
  pacing_on || return 0
  local cls="${1:-clean}" streak
  streak="$(pace_streak_get)"
  case "$streak" in ''|*[!0-9]*) streak=0;; esac
  if [ "$cls" = degraded ]; then
    streak=$(( streak + 1 ))
    echo "$streak" > "$BENCH_PACE_STATE" 2>/dev/null || true
    echo "[pace] degraded session ($streak/$BENCH_DEGRADED_STREAK_LIMIT consecutive)" >&2
    if [ "$streak" -ge "$BENCH_DEGRADED_STREAK_LIMIT" ]; then
      echo "[pace] *** $streak consecutive degraded sessions — cooling down ${BENCH_WINDOW_COOLDOWN_SECONDS}s for a window reset ***" >&2
      sleep "$BENCH_WINDOW_COOLDOWN_SECONDS"
      pace_streak_reset
      echo "[pace] cooldown complete; resuming with a fresh window" >&2
    fi
  else
    [ "$streak" != 0 ] && echo "[pace] clean session — resetting degraded streak (was $streak)" >&2
    pace_streak_reset
  fi
}
