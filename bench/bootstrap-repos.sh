#!/usr/bin/env bash
set -euo pipefail

# Bootstrap reference copies of benchmark repos into $SENSE_BENCH_ROOT/_reference/.
#
# These are the "source of truth" copies that ground-truth is generated against.
# Per-tool copies are created by scan.sh/run.sh using git clone --reference.
#
# Usage:
#   bash bench/bootstrap-repos.sh               # all repos
#   bash bench/bootstrap-repos.sh --repo flask  # single repo

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
PINNED="$BENCH_DIR/PINNED_COMMITS.json"

SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"
REF_DIR="$SENSE_BENCH_ROOT/_reference"

FILTER_REPOS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) FILTER_REPOS="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: bootstrap-repos.sh [--repo r1,r2]"
      echo ""
      echo "Clones benchmark repos into \$SENSE_BENCH_ROOT/_reference/ at pinned commits."
      echo "Default SENSE_BENCH_ROOT: ../sense-benchmark (sibling of the sense project)"
      echo ""
      echo "Options:"
      echo "  --repo  Comma-separated repo filter (e.g. flask,discourse)"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

matches_filter() {
  local value="$1" filter="$2"
  [[ -z "$filter" ]] && return 0
  echo "$filter" | tr ',' '\n' | grep -qx "$value"
}

timestamp() { date +%Y-%m-%dT%H:%M:%S; }
log() { echo "[$(timestamp)] $*" >&2; }

repos=$(python3 -c "
import json, sys
data = json.load(open(sys.argv[1]))
for k in data:
    if k.startswith('_'): continue
    print(k)
" "$PINNED")

mkdir -p "$REF_DIR"

cloned=0
skipped=0
for repo in $repos; do
  matches_filter "$repo" "$FILTER_REPOS" || continue

  remote=$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]]['remote'])" "$PINNED" "$repo")
  commit=$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]]['commit'])" "$PINNED" "$repo")

  dest="$REF_DIR/$repo"

  if [[ -d "$dest/.git" ]]; then
    actual=$(cd "$dest" && git rev-parse HEAD 2>/dev/null || echo "")
    if [[ "$actual" == "$commit" ]]; then
      log "$repo: already at pinned commit ${commit:0:12} — skipping"
      skipped=$((skipped + 1))
      continue
    fi
    log "$repo: exists but at wrong commit — fetching and resetting..."
    (cd "$dest" && git fetch origin "$commit" && git -c advice.detachedHead=false checkout "$commit" --quiet)
    cloned=$((cloned + 1))
    continue
  fi

  log "$repo: cloning from $remote..."
  if ! git clone --quiet "$remote" "$dest"; then
    rm -rf "$dest"
    log "$repo: clone FAILED — cleaned up partial directory"
    continue
  fi
  (cd "$dest" && git -c advice.detachedHead=false checkout "$commit" --quiet)
  log "$repo: checked out at ${commit:0:12}"
  cloned=$((cloned + 1))
done

log ""
log "=== Bootstrap complete ==="
log "  Reference dir: $REF_DIR"
log "  Cloned/updated: $cloned | Already pinned: $skipped"
