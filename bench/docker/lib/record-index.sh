#!/usr/bin/env bash
# record-index.sh — append one row to /bench/index-info.tsv with the
# state of <tool>'s index for <repo>. Called from each tool Dockerfile
# immediately after the indexing step so that build-time stats are
# captured into the image. entrypoint.sh prints the row at session start
# so the operator sees "this index was built T seconds ago, contains
# N symbols, lives in image layer X" without grepping the build log.
#
# Usage: record-index.sh <tool> <repo> <build_seconds>
#
# The index is persistent inside the image — built once at `docker build`
# time, immutable across every `docker run`. A fresh container starts
# with the same index every time; rebuilding the image (or changing the
# pinned commit) is the only way to refresh it.
set -euo pipefail

TOOL="${1:?usage: record-index.sh <tool> <repo> <build_seconds>}"
REPO="${2:?usage: record-index.sh <tool> <repo> <build_seconds>}"
BUILD_SECS="${3:?usage: record-index.sh <tool> <repo> <build_seconds>}"

INFO_DIR="/bench"
INFO_FILE="$INFO_DIR/index-info.tsv"
mkdir -p "$INFO_DIR"

# Initialize TSV header on first write — keeps the file readable with
# `column -t` / `awk` without a separate manifest.
if [[ ! -f "$INFO_FILE" ]]; then
  printf 'tool\trepo\tsymbols\tfiles\tembeddings\tindex_bytes\tbuild_seconds\tbuilt_at\n' > "$INFO_FILE"
fi

REPO_DIR="/repos/$REPO"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
SYMBOLS="-"
FILES="-"
EMBEDDINGS="-"
INDEX_BYTES="-"

case "$TOOL" in
  sense)
    # `sense status --json` reports the canonical numbers — same shape
    # used by run.sh's host check_ready in its prior life.
    if status_json=$(cd "$REPO_DIR" && sense status --json 2>/dev/null); then
      SYMBOLS=$(echo "$status_json" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["index"]["symbols"])')
      FILES=$(echo "$status_json"   | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["index"]["files"])')
      EMBEDDINGS=$(echo "$status_json" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["index"]["embeddings"])')
    fi
    if [[ -d "$REPO_DIR/.sense" ]]; then
      INDEX_BYTES=$(du -sb "$REPO_DIR/.sense" 2>/dev/null | awk '{print $1}')
    fi
    ;;
  serena)
    # Serena's cache lives under .serena/cache/<language>/. We count
    # files there as a proxy for "symbol-cache populated" — the same
    # signal the old check_ready used.
    if [[ -d "$REPO_DIR/.serena/cache" ]]; then
      FILES=$(find "$REPO_DIR/.serena/cache" -type f 2>/dev/null | wc -l | tr -d ' ')
      INDEX_BYTES=$(du -sb "$REPO_DIR/.serena" 2>/dev/null | awk '{print $1}')
    fi
    ;;
  gitnexus)
    # GitNexus writes a .gitnexus/ directory with embeddings + graph
    # artefacts. No public "stats" command yet, so we report file count
    # and total bytes as a coarse "index is here" signal.
    if [[ -d "$REPO_DIR/.gitnexus" ]]; then
      FILES=$(find "$REPO_DIR/.gitnexus" -type f 2>/dev/null | wc -l | tr -d ' ')
      INDEX_BYTES=$(du -sb "$REPO_DIR/.gitnexus" 2>/dev/null | awk '{print $1}')
    fi
    ;;
  probe)
    # Stateless — every query parses on the fly. No persistent index.
    SYMBOLS="(stateless)"
    FILES="(stateless)"
    EMBEDDINGS="(stateless)"
    INDEX_BYTES="0"
    ;;
  baseline)
    SYMBOLS="(none)"
    FILES="(none)"
    EMBEDDINGS="(none)"
    INDEX_BYTES="0"
    ;;
esac

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
  "$TOOL" "$REPO" "$SYMBOLS" "$FILES" "$EMBEDDINGS" "$INDEX_BYTES" "$BUILD_SECS" "$BUILT_AT" \
  >> "$INFO_FILE"
