# syntax=docker/dockerfile:1.4
# bench-sense: sense binary built from the local working tree + pre-built
# .sense/ indexes for all 6 repos.
#
# Build from the repo root:
#   docker build -f bench/global/docker/sense/Dockerfile -t bench-sense:dev .
#
# The sense binary embeds platform ORT shared libraries via go:embed, so the
# runtime stage carries no extra C dependencies. Build-time fetch-deps.sh
# downloads model.onnx, reranker.onnx, vocab.txt, and per-platform ORT libs
# into internal/embed/bundle/ before `go build` so the embeds resolve.

# Global build args (declared before any FROM so they're visible in every stage).
ARG GO_VERSION=1.25
ARG BASE_TAG=dev
ARG VERSION=dev

# ── Stage 1: build sense from local source ─────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS builder

ENV DEBIAN_FRONTEND=noninteractive
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get update && apt-get install -y --no-install-recommends \
      curl ca-certificates git make

WORKDIR /src

# Cache go module download separately from source so a code edit doesn't bust
# the module cache layer. The cache mount also survives parent-layer
# invalidations — useful when bumping GO_VERSION.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download

# Copy the rest of the source. .dockerignore drops bench/results, .git, dist,
# internal/embed/testdata, etc.
COPY . .

# `make build` runs scripts/fetch-deps.sh (writes into internal/embed/bundle/)
# and then `go build -o bin/sense ./cmd/sense`. The binary embeds all four
# platform ORT libs so the runtime image needs no further ORT install.
# /root/.cache/go-build persists the Go build cache across rebuilds so
# incremental sense edits don't redo every package compile.
ARG VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    make build VERSION=${VERSION} && /src/bin/sense --version

# ── Stage 2: tool runtime on bench-baseline ────────────────────────────────
FROM bench-baseline:${BASE_TAG}
ARG VERSION

COPY --from=builder /src/bin/sense /usr/local/bin/sense
RUN sense --version

# Pre-build indexes for all 6 repos. One layer per repo so a single pinned
# commit bump only invalidates that repo's index layer. sense scan -embed
# does parsing + embedding in one pass — no deferred work, so the freshness
# check `embeddings == symbols && stale_files == 0` is true the moment scan
# returns. nextjs scan ~20min; flask/gin/javalin/axum are minutes each.
#
# record-index.sh appends a row to /bench/index-info.tsv per repo with the
# build duration + symbol/embedding counts. entrypoint.sh prints the row
# at session start so the operator sees what's indexed and confirms it is
# persistent (immutable across `docker run`; refreshed only by rebuild).
RUN t0=$(date +%s); echo "[sense] indexing /repos/axum ..."; \
    sense scan --dir /repos/axum -embed && \
    /usr/local/lib/bench/record-index.sh sense axum $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[sense] indexing /repos/discourse ..."; \
    sense scan --dir /repos/discourse -embed && \
    /usr/local/lib/bench/record-index.sh sense discourse $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[sense] indexing /repos/flask ..."; \
    sense scan --dir /repos/flask -embed && \
    /usr/local/lib/bench/record-index.sh sense flask $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[sense] indexing /repos/gin ..."; \
    sense scan --dir /repos/gin -embed && \
    /usr/local/lib/bench/record-index.sh sense gin $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[sense] indexing /repos/javalin ..."; \
    sense scan --dir /repos/javalin -embed && \
    /usr/local/lib/bench/record-index.sh sense javalin $(( $(date +%s) - t0 ))
# nextjs: stage the host-built .sense into /repos/nextjs/.sense. The from-
# scratch scan+embed for nextjs (75k+ symbols) peaks above what Docker
# Desktop's VM can give a single process (>16 GB observed, OOMs even
# with swap). Doing the scan on the host once — where full system RAM
# is available — and shipping the resulting `.sense` directory avoids
# that ceiling.
#
# We do NOT re-run `sense scan -embed` here. Even when nothing is stale,
# the scan path falls through to a full HNSW rebuild for all 75k vectors
# (internal/scan/embed.go: tryIncrementalHNSW returns false when
# changedFileIDs is empty, causing the caller to rebuild from scratch).
# That rebuild is both slow and memory-intensive — re-introducing the
# very OOM we're trying to avoid. The host's already-built hnsw.bin is
# valid; we just ship it. MCP queries at scoring time read the staged
# .sense directly.
#
# `bench/build-prescan.sh` (run automatically by `bench/build.sh` before
# this image builds) populates bench/global/docker/sense/preindex/nextjs.sense/
# from $SENSE_BENCH_ROOT/sense/nextjs/.sense/ via rsync. The COPY layer
# below invalidates whenever that staged copy changes, so a host re-scan
# correctly triggers an image rebuild for this step only. record-index.sh
# is run separately (no t0/elapsed since there's no in-container scan).
COPY bench/global/docker/sense/preindex/nextjs.sense /repos/nextjs/.sense
RUN /usr/local/lib/bench/record-index.sh sense nextjs 0

# Per-repo Claude Code integration via `sense setup` — the same command an
# end-user runs after `sense scan`. Writes CLAUDE.md (the canonical
# senseSection from internal/setup/marker.go), .mcp.json, .claude/skills/,
# .claude/agents/, and the .claude/settings.json hook block (pre-tool-use,
# pre-compact, subagent-start, session-start, post-tool-use). The bench
# now runs sense exactly as it ships, hooks included.
RUN set -eux && \
    for repo in axum discourse flask gin javalin nextjs; do \
      cd /repos/$repo && sense setup --tools claude-code ; \
    done

ENV BENCH_TOOL=sense
ENV BENCH_TOOL_VERSION=${VERSION}
