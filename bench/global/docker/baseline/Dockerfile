# syntax=docker/dockerfile:1.4
# bench-baseline: claude CLI + 6 reference repos + the "no MCP tools" config.
#
# Two roles in one image:
#   1. Foundation — every other tool image does `FROM bench-baseline` and
#      adds its own binary + indexes + per-repo overrides on top. The
#      shared bottom layers (claude CLI, repos at pinned SHAs, bench
#      scaffolding) are reused across all tool images.
#   2. Scoring target — "what does Claude Code do without any code-intel
#      MCP server?" is the no-MCP control in the bench. Running this
#      image directly answers that question; tool images override BENCH_TOOL
#      and overwrite the per-repo CLAUDE.md/.mcp.json to swap behavior in.
#
# Build from the repo root:
#   docker build -f bench/global/docker/baseline/Dockerfile -t bench-baseline:dev .
#
# The build context must be the repo root so the COPY commands below can
# reach bench/scenarios/, bench/lib/, bench/global/docker/entrypoint.sh,
# bench/PINNED_COMMITS.json.

FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=/root/.local/bin:/usr/local/bin:/usr/bin:/bin

# OS dependencies. python3-yaml is bench/lib/scenario.py's only runtime dep.
# tini reaps zombies if claude spawns subprocesses that escape.
#
# BuildKit cache mounts persist /var/cache/apt and /var/lib/apt across
# builds so a base-layer rebuild doesn't re-download the same .debs every
# time.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      bash \
      python3 \
      python3-yaml \
      tini \
      coreutils

# Claude CLI — Linux build via the official install script. The script
# detects the platform (linux-x64 / linux-arm64) and writes to ~/.local/bin.
RUN curl -fsSL https://claude.ai/install.sh -o /tmp/claude-install.sh \
 && bash /tmp/claude-install.sh \
 && rm /tmp/claude-install.sh \
 && claude --version

# Empty ~/.claude — the whole point of containerizing is to never see the
# host's hooks, MCP servers, or credentials. Entrypoint expects
# ANTHROPIC_API_KEY via -e at `docker run` time; OAuth subscription
# fallback (no browser inside the container) is intentionally disabled.
RUN mkdir -p /root/.claude \
 && printf '{"hooks":[]}\n' > /root/.claude/settings.json

# Reference repos at pinned commits, shallow-fetched at the target SHA.
# `git fetch --depth 1 <SHA>` then `git checkout FETCH_HEAD` fetches only
# the commit's tree — next.js drops from 2.93GB to ~500MB, discourse from
# 1.19GB to ~300MB. Bench scoring only touches HEAD's working tree (no
# `git log`, no `git diff` against history); `git rev-parse --short HEAD`
# for the repo_commit field works on shallow clones too.
#
# GitHub allows `git fetch <SHA>` since 2020 (uploadpack.allowAnySHA1InWant).
# One RUN per repo so a single commit bump invalidates only that repo's
# layer, not all six.
RUN git -c init.defaultBranch=main init /repos/axum \
 && git -C /repos/axum remote add origin https://github.com/tokio-rs/axum.git \
 && git -C /repos/axum fetch --depth 1 --no-tags origin 9f4d52e310a6053e49f3db09ce02f723a7a3f5bb \
 && git -C /repos/axum -c advice.detachedHead=false checkout FETCH_HEAD

RUN git -c init.defaultBranch=main init /repos/discourse \
 && git -C /repos/discourse remote add origin https://github.com/discourse/discourse.git \
 && git -C /repos/discourse fetch --depth 1 --no-tags origin d73e4484b4b9dcffa1e75e2ceff6e0a005d479c6 \
 && git -C /repos/discourse -c advice.detachedHead=false checkout FETCH_HEAD

RUN git -c init.defaultBranch=main init /repos/flask \
 && git -C /repos/flask remote add origin https://github.com/pallets/flask.git \
 && git -C /repos/flask fetch --depth 1 --no-tags origin 2ac89889f4cc330eabd50f295dcef02828522c69 \
 && git -C /repos/flask -c advice.detachedHead=false checkout FETCH_HEAD

RUN git -c init.defaultBranch=main init /repos/gin \
 && git -C /repos/gin remote add origin https://github.com/gin-gonic/gin.git \
 && git -C /repos/gin fetch --depth 1 --no-tags origin d3ffc9985281dcf4d3bef604cce4e662b1a327a6 \
 && git -C /repos/gin -c advice.detachedHead=false checkout FETCH_HEAD

RUN git -c init.defaultBranch=main init /repos/javalin \
 && git -C /repos/javalin remote add origin https://github.com/javalin/javalin.git \
 && git -C /repos/javalin fetch --depth 1 --no-tags origin 7078afcc794b2a55e7615a14c250ee1330c52999 \
 && git -C /repos/javalin -c advice.detachedHead=false checkout FETCH_HEAD

RUN git -c init.defaultBranch=main init /repos/nextjs \
 && git -C /repos/nextjs remote add origin https://github.com/vercel/next.js.git \
 && git -C /repos/nextjs fetch --depth 1 --no-tags origin 16e5f9e685188751f1f4fb085f6e9e729f409950 \
 && git -C /repos/nextjs -c advice.detachedHead=false checkout FETCH_HEAD

# Bench scaffolding (scenario YAMLs, scenario.py, scorer.py for budget/timeout
# defaults). Copied last so an edit to scenarios doesn't bust the repo layers.
COPY bench/PINNED_COMMITS.json /bench/PINNED_COMMITS.json
COPY bench/scenarios/         /bench/scenarios/
COPY bench/lib/               /bench/lib/
COPY bench/global/docker/entrypoint.sh /usr/local/bin/entrypoint.sh
# Per-tool image helpers: record-index.sh appends one row per (tool,repo)
# to /bench/index-info.tsv at build time so the operator can see when each
# index was built, how big it is, and that it's persistent in the image.
COPY bench/global/docker/lib/         /usr/local/lib/bench/
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/lib/bench/*.sh

# Baseline per-repo config: the "no MCP server" control. Same content for
# all 6 repos — Claude Code auto-discovers $cwd/CLAUDE.md and $cwd/.mcp.json,
# and entrypoint.sh sets cwd to /repos/$REPO. Tool images that build
# `FROM bench-baseline` overwrite these files with their own tool-specific
# wiring; this is the floor.
COPY bench/global/docker/baseline/CLAUDE.md /tmp/CLAUDE.md
RUN set -eux && \
    for repo in axum discourse flask gin javalin nextjs; do \
      cp /tmp/CLAUDE.md /repos/$repo/CLAUDE.md && \
      printf '{"mcpServers":{}}\n' > /repos/$repo/.mcp.json && \
      mkdir -p /repos/$repo/.claude && \
      printf '{"hooks":[]}\n' > /repos/$repo/.claude/settings.json && \
      /usr/local/lib/bench/record-index.sh baseline "$repo" 0 ; \
    done && \
    rm /tmp/CLAUDE.md

ENV BENCH_TOOL=baseline
ENV BENCH_TOOL_VERSION=

WORKDIR /repos
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint.sh"]
