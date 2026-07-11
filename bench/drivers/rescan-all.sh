#!/usr/bin/env bash
# rescan-all.sh — (re)index every official vertical repo in SMALLEST→BIGGEST
# order, reporting progress as it goes.
#
# Repo membership comes from the per-vertical repos.txt files
# (bench/verticals/<vertical>/repos.txt) — the single source of truth for what
# each vertical runs. DEFAULT = the union of EVERY vertical; scope with
# VERTICALS and/or REPOS (both are space-separated lists, REPOS filters after
# VERTICALS). A repo may belong to several verticals; the union is deduped.
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
# Why smallest-first (the opposite of the BENCH order, which lives in
# sweep-resume.sh): a rescan is a one-time index build, not a discrimination
# test. Cheap indexes (raix, langchainrb, …) finish in seconds and prove the
# binary + toolchain are healthy before the multi-hour gitlabhq (177k symbols)
# embed runs LAST. If something is broken you learn it on a 5-second repo, not
# 2 hours into the big one.
#
#   bash bench/drivers/rescan-all.sh                 # FULL rebuild+embed of every vertical repo (default)
#   SKIP_FRESH=1 bash bench/drivers/rescan-all.sh    # replayable: skip fingerprint-fresh, rebuild only stale
#   VERTICALS="python-django" bash bench/drivers/rescan-all.sh       # one vertical only
#   VERTICALS="ruby-rails python-django" bash bench/drivers/rescan-all.sh   # several verticals
#   REPOS="forem rails" bash bench/drivers/rescan-all.sh   # subset of repos, still in smallest-first order
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

# --- vertical selection --------------------------------------------------
VERT_DIR="$BENCH_DIR/verticals"
verticals=()
if [ -n "${VERTICALS:-}" ]; then
  for v in $VERTICALS; do
    if [ ! -f "$VERT_DIR/$v/repos.txt" ]; then
      echo "[rescan] unknown vertical '$v' (no $VERT_DIR/$v/repos.txt); available:" \
           "$(ls "$VERT_DIR" 2>/dev/null | while read -r d; do [ -f "$VERT_DIR/$d/repos.txt" ] && printf '%s ' "$d"; done)" >&2
      exit 2
    fi
    verticals+=("$v")
  done
else
  for f in "$VERT_DIR"/*/repos.txt; do
    [ -f "$f" ] && verticals+=("$(basename "$(dirname "$f")")")
  done
fi
[ "${#verticals[@]}" -eq 0 ] && { echo "[rescan] no verticals found under $VERT_DIR" >&2; exit 2; }

# Union of the selected verticals' repos.txt (one repo key per line, comments
# allowed), deduped — membership lists are not a partition.
repo_keys=()
for v in "${verticals[@]}"; do
  while IFS= read -r line; do
    line="${line%%#*}"
    line="${line//[[:space:]]/}"
    [ -n "$line" ] && repo_keys+=("$line")
  done < "$VERT_DIR/$v/repos.txt"
done
[ "${#repo_keys[@]}" -eq 0 ] && { echo "[rescan] selected verticals list no repos (${verticals[*]})" >&2; exit 2; }

# SMALLEST→BIGGEST by index.db size on disk — the same rule as
# ensure-index.sh --all: size exists for every already-indexed repo, and a
# missing index sorts as 0 so a never-indexed repo fails fast instead of after
# the giants. Name breaks ties.
SMALL_TO_BIG=()
while IFS= read -r r; do [ -n "$r" ] && SMALL_TO_BIG+=("$r"); done < <(
  SENSE_CLONES="$CLONES" python3 -c "import os,sys
clones=os.environ['SENSE_CLONES']
repos=list(dict.fromkeys(sys.argv[1:]))
def size(r):
    try: return os.path.getsize(os.path.join(clones,r,'.sense','index.db'))
    except OSError: return 0
print('\n'.join(sorted(repos,key=lambda r:(size(r),r))))" "${repo_keys[@]}")

# Optional REPOS override: keep only the requested repos, preserving small-first order.
if [ -n "${REPOS:-}" ]; then
  filtered=()
  for r in "${SMALL_TO_BIG[@]}"; do
    for want in $REPOS; do [ "$r" = "$want" ] && filtered+=("$r"); done
  done
  [ "${#filtered[@]}" -eq 0 ] && { echo "[rescan] no repos selected (VERTICALS='${VERTICALS:-all}' REPOS='$REPOS')" >&2; exit 2; }
  SMALL_TO_BIG=("${filtered[@]}")
fi

CHECK_FLAG=""
[ "${1:-}" = "--check" ] && CHECK_FLAG="--check"

total="${#SMALL_TO_BIG[@]}"
echo "[rescan] verticals: ${verticals[*]}"
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
