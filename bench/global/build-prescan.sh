#!/usr/bin/env bash
# build-prescan.sh — stage host-built indexes into the docker build context.
#
# Some per-repo indexing steps (sense scan + embed on nextjs is the only
# one today) don't fit inside Docker Desktop's memory cap on a typical
# developer machine — the embed pass for 75k+ symbols peaks above what
# the VM has, OOMs, and dies after burning hours on swap. The host has
# full RAM; pre-indexing there once and copying the resulting `.sense`
# into the image at build time costs ~1 minute (network of bytes) and
# avoids the OOM entirely. The container then runs `sense scan -embed`
# as a freshness check on top of the staged index, which is near-instant
# when nothing's stale.
#
# Source layout (host-side, populated by the operator running `sense
# scan` locally — typically already present from earlier dev work):
#
#   $SENSE_BENCH_ROOT/sense/<repo>/.sense/
#
# Destination layout (inside the docker build context so `COPY` works):
#
#   bench/global/docker/sense/preindex/<repo>.sense/
#
# The Dockerfile COPYs from the destination into /repos/<repo>/.sense.
# Idempotent — rsync only updates changed files. Gitignored at the
# bench/global/docker/sense level so the staged indexes don't get committed.

set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
SENSE_BENCH_ROOT="${SENSE_BENCH_ROOT:-$(cd "$PROJECT_ROOT/.." && pwd)/sense-benchmark}"

PREINDEX_DIR="$BENCH_DIR/global/docker/sense/preindex"

# Repos for which we pre-stage from host instead of indexing inside the
# image. Add others here as their in-container scan turns out to OOM.
PRESTAGE_REPOS=(nextjs)

timestamp() { date +%H:%M:%S; }
log() { echo "[$(timestamp)] $*" >&2; }

mkdir -p "$PREINDEX_DIR"

for repo in "${PRESTAGE_REPOS[@]}"; do
  src="$SENSE_BENCH_ROOT/sense/$repo/.sense"
  dst="$PREINDEX_DIR/$repo.sense"

  if [[ ! -d "$src" ]]; then
    cat >&2 <<EOF
[prescan] ERROR: $src does not exist.
[prescan] To stage the $repo index for the docker build, run:
[prescan]   cd $SENSE_BENCH_ROOT/sense/$repo
[prescan]   sense scan -embed
[prescan] then re-run this script.
EOF
    exit 1
  fi

  # `index.db` is the canonical artefact. Without it, the .sense dir is
  # incomplete (e.g. an in-flight scan that was killed mid-way).
  if [[ ! -f "$src/index.db" ]]; then
    log "ERROR: $src/index.db missing — host scan looks incomplete; aborting"
    exit 1
  fi

  log "staging $repo: $src → $dst"
  mkdir -p "$dst"
  # --delete so a host re-scan with fewer artefacts is mirrored
  # cleanly (no leftover stale files in the staged copy).
  # --times so file mtimes survive — sense's freshness check looks
  # at file_mtime; preserving them lets `sense scan -embed` in the
  # container see "already fresh" and skip the embed pass.
  rsync -a --delete --times "$src/" "$dst/"
done

# Top-level .gitignore on the preindex dir so the (potentially large)
# staged artefacts never get committed. Idempotent: rewriting the same
# content on every invocation is harmless.
cat > "$PREINDEX_DIR/.gitignore" <<'EOF'
# Staged per-repo sense indexes — populated by bench/global/build-prescan.sh
# from $SENSE_BENCH_ROOT/sense/<repo>/.sense/ on the host. Regenerable;
# not part of the bench source.
*
!.gitignore
EOF

log "prescan complete — staged: ${PRESTAGE_REPOS[*]}"
