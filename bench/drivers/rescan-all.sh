#!/usr/bin/env bash
# rescan-all.sh — (re)index every Ruby/Rails-vertical repo in SMALLEST→BIGGEST
# order, reporting progress as it goes.
#
# DEFAULT = a real full rebuild+embed of EVERY repo: each launched scan is
# `sense scan -rebuild -embed` (this is what "rescan" means here — re-resolve the
# scan-layer edges AND regenerate embeddings, so a marked-fresh-but-unembedded
# index can't be silently served). The fingerprint skip is OFF by default; turn it
# on with SKIP_FRESH=1 for a replayable gap-fill that resumes after an interruption.
#
# Per repo it first runs `sense setup` (auto-detect all tools) to refresh the
# tool-integration files in the clone — done every time, whether or not the index
# is rebuilt — then ensures the index. (--check skips setup: it is read-only.)
#
# Why smallest-first (the opposite of the BENCH order): a rescan is a one-time
# index build, not a discrimination test. Cheap indexes (raix, langchainrb, …)
# finish in seconds and prove the binary + toolchain are healthy before the
# multi-hour gitlabhq (177k symbols) embed runs LAST. If something is broken you
# learn it on a 5-second repo, not 2 hours into the big one.
#
#   bash bench/drivers/rescan-all.sh                 # FULL rebuild+embed of every repo (default)
#   SKIP_FRESH=1 bash bench/drivers/rescan-all.sh    # replayable: skip fingerprint-fresh, rebuild only stale
#   REPOS="forem rails" bash bench/drivers/rescan-all.sh   # subset, still in smallest-first order
#   bash bench/drivers/rescan-all.sh --check         # report freshness only, rebuild nothing
#
# Knobs: SENSE_BIN (default ~/.local/bin/sense), SENSE_CLONES (clone root). The
# actual scan command lives in ensure-index.sh (`sense scan -rebuild -embed`).

set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$BENCH_DIR/.."

SENSE_BIN="${SENSE_BIN:-$HOME/.local/bin/sense}"
CLONES="${SENSE_CLONES:-$HOME/Developer/luuuc/oss/sense-benchmark/sense}"

# Default to a real rebuild+embed of every repo; SKIP_FRESH=1 restores the
# fingerprint-skip (replayable gap-fill). --check never forces (reports true staleness).
if [ "${1:-}" != "--check" ] && [ "${SKIP_FRESH:-0}" != 1 ]; then
  export FORCE_REBUILD=1
fi

# Canonical vertical repos, SMALLEST→BIGGEST by indexed-symbol count (see
# bench/index-state.json). Keep this in sync if the lineup changes; the BENCH
# order (biggest↔smallest interleave) lives in sweep-resume.sh, deliberately
# different — see that file's header.
SMALL_TO_BIG=(raix langchainrb lobsters ruby_llm llm.rb solidus redmine chatwoot mastodon forem rails discourse gitlabhq)

# Optional REPOS override: keep only the requested repos, preserving small-first order.
if [ -n "${REPOS:-}" ]; then
  filtered=()
  for r in "${SMALL_TO_BIG[@]}"; do
    for want in $REPOS; do [ "$r" = "$want" ] && filtered+=("$r"); done
  done
  SMALL_TO_BIG=("${filtered[@]}")
fi

CHECK_FLAG=""
[ "${1:-}" = "--check" ] && CHECK_FLAG="--check"

total="${#SMALL_TO_BIG[@]}"
echo "[rescan] $total repos, smallest→biggest order"
echo "[rescan] $(${SENSE_BIN:-$HOME/.local/bin/sense} --version 2>/dev/null | head -1)"
if [ -n "$CHECK_FLAG" ]; then echo "[rescan] mode: --check (report staleness, rebuild nothing)"
elif [ "${FORCE_REBUILD:-0}" = 1 ]; then echo "[rescan] mode: FULL rebuild+embed of every repo (sense scan -rebuild -embed)"
else echo "[rescan] mode: SKIP_FRESH — skip fingerprint-fresh, rebuild only stale (replayable)"
fi
echo

i=0
fail=0
start_all=$(date +%s)
for repo in "${SMALL_TO_BIG[@]}"; do
  i=$((i+1))
  start=$(date +%s)
  echo "──────────────────────────────────────────────────────────────"
  echo "[$i/$total] $repo  start $(date +%H:%M:%S)"
  # Refresh the Sense tool-integration files (CLAUDE.md, AGENTS.md, .mcp.json,
  # opencode.json, .codex/config.toml, …) before each rescan — whether or not the
  # index itself gets rebuilt — so every clone benches with current integration.
  # Skipped in --check mode (read-only: setup writes files into the clone).
  if [ -z "$CHECK_FLAG" ]; then
    if [ -d "$CLONES/$repo" ]; then
      ( cd "$CLONES/$repo" && "$SENSE_BIN" setup >/dev/null 2>&1 ) \
        && echo "[setup ] $repo — integration files refreshed" \
        || echo "[setup ] $repo — WARN: sense setup failed (continuing)" >&2
    else
      echo "[setup ] $repo — WARN: clone $CLONES/$repo not found (skipping setup)" >&2
    fi
  fi
  if bash "$BENCH_DIR/lib/ensure-index.sh" $CHECK_FLAG "$repo"; then
    dur=$(( $(date +%s) - start ))
    echo "[$i/$total] $repo  done in ${dur}s"
  else
    dur=$(( $(date +%s) - start ))
    if [ -n "$CHECK_FLAG" ]; then
      echo "[$i/$total] $repo  STALE — would rebuild (${dur}s)"   # informational in --check mode
    else
      echo "[$i/$total] $repo  FAILED (rebuild exit nonzero) after ${dur}s"
    fi
    fail=$((fail+1))
  fi
  echo
done

total_dur=$(( $(date +%s) - start_all ))
echo "──────────────────────────────────────────────────────────────"
if [ -n "$CHECK_FLAG" ]; then
  echo "[rescan] check complete in ${total_dur}s — $((total-fail))/$total fresh, $fail would rebuild"
else
  echo "[rescan] complete in ${total_dur}s — $((total-fail))/$total ok, $fail need attention"
fi
[ "$fail" -gt 0 ] && exit 1 || exit 0
