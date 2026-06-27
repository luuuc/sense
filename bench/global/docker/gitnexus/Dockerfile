# syntax=docker/dockerfile:1.4
# bench-gitnexus: gitnexus MCP server + pre-built .gitnexus/ per repo.
#
# Build from the repo root:
#   docker build -f bench/global/docker/gitnexus/Dockerfile -t bench-gitnexus:dev .
#
# Pre-indexing all 6 repos at build time is slow (gitnexus downloads a
# HuggingFace embedding model on first run + analyzes each repo). That cost
# is paid once per image, never at runtime.

ARG BASE_TAG=dev
FROM bench-baseline:${BASE_TAG}

# Node + gitnexus. gitnexus bundles tree-sitter grammars that build native
# .node addons via node-gyp, so build-essential (make + g++) is mandatory
# at install time. python3 is already present in the base image and
# node-gyp picks it up.
#
# Pinned to Node 20: Node 24's headers require C++20, but tree-sitter@0.21's
# gyp config compiles without `-std=c++20` and fails on Node 24. Node 20 is
# the previous LTS and the version gitnexus was developed against.
#
# After `npm install -g` completes, the compiled .node addons are baked
# into the global node_modules. build-essential is no longer needed at
# runtime, so we `apt-get purge` it in the same RUN — the layer ships
# without ~300MB of compilers. Cache mounts persist apt+npm caches across
# rebuilds.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    --mount=type=cache,target=/root/.npm,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
 && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
 && apt-get install -y --no-install-recommends nodejs build-essential \
 && npm install -g gitnexus@latest \
 && gitnexus --version \
 && apt-get purge -y build-essential \
 && apt-get autoremove -y

# Pre-build the .gitnexus/ index for each repo. `--skills` is the upstream
# recommended flag for Claude Code integration: it indexes the codebase,
# detects functional clusters, writes a SKILL.md per cluster under
# .claude/skills/generated/, and writes the canonical `gitnexus:start`
# block into AGENTS.md/CLAUDE.md with the agent's operating discipline
# (MUST run impact before editing, MUST run detect_changes before commit,
# risk levels, etc.). Bench used to overwrite these with a thinned tool
# catalog; running gitnexus exactly as upstream ships it removes that
# asymmetry. One layer per repo so a single pinned-commit bump (which
# invalidates that repo's clone layer in bench-baseline) rebuilds only that
# tool layer here.
RUN t0=$(date +%s); echo "[gitnexus] indexing /repos/axum ..."; \
    cd /repos/axum && gitnexus analyze --embeddings --skills && \
    /usr/local/lib/bench/record-index.sh gitnexus axum $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[gitnexus] indexing /repos/discourse ..."; \
    cd /repos/discourse && gitnexus analyze --embeddings --skills && \
    /usr/local/lib/bench/record-index.sh gitnexus discourse $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[gitnexus] indexing /repos/flask ..."; \
    cd /repos/flask && gitnexus analyze --embeddings --skills && \
    /usr/local/lib/bench/record-index.sh gitnexus flask $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[gitnexus] indexing /repos/gin ..."; \
    cd /repos/gin && gitnexus analyze --embeddings --skills && \
    /usr/local/lib/bench/record-index.sh gitnexus gin $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[gitnexus] indexing /repos/javalin ..."; \
    cd /repos/javalin && gitnexus analyze --embeddings --skills && \
    /usr/local/lib/bench/record-index.sh gitnexus javalin $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[gitnexus] indexing /repos/nextjs ..."; \
    cd /repos/nextjs && gitnexus analyze --embeddings --skills && \
    /usr/local/lib/bench/record-index.sh gitnexus nextjs $(( $(date +%s) - t0 ))

# `gitnexus analyze --skills` already wrote AGENTS.md, CLAUDE.md, and the
# per-cluster SKILL.md files. We only add the .mcp.json (so Claude Code
# wires the MCP server) and leave .claude/settings.json with empty hooks
# (gitnexus's recommended PreToolUse/PostToolUse hooks are skipped: this
# bench is a static one-shot, so they have no scoring-session effect).
COPY bench/global/docker/gitnexus/mcp.json /tmp/mcp.json
RUN set -eux && \
    for repo in axum discourse flask gin javalin nextjs; do \
      cp /tmp/mcp.json /repos/$repo/.mcp.json && \
      mkdir -p /repos/$repo/.claude && \
      [[ -f /repos/$repo/.claude/settings.json ]] || \
        printf '{"hooks":[]}\n' > /repos/$repo/.claude/settings.json ; \
    done && \
    rm /tmp/mcp.json

ENV BENCH_TOOL=gitnexus
