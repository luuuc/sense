# syntax=docker/dockerfile:1.4
# bench-probe: probe MCP server, stateless (no index baked).
#
# Build from the repo root:
#   docker build -f bench/global/docker/probe/Dockerfile -t bench-probe:dev .

ARG BASE_TAG=dev
FROM bench-baseline:${BASE_TAG}

# Node + probe. Debian's nodejs LTS is too old; use Nodesource for current LTS.
# Cache mounts let `npm install -g @probelabs/probe@latest` reuse downloaded
# tarballs across builds — meaningful when bumping probe version often.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    --mount=type=cache,target=/root/.npm,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
 && curl -fsSL https://deb.nodesource.com/setup_lts.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && npm install -g @probelabs/probe@latest \
 && probe --version

# Per-repo CLAUDE.md + probe .mcp.json + empty hooks. Claude auto-discovers
# $cwd/{CLAUDE.md,.mcp.json,.claude/settings.json}.
#
# Probe is stateless — every MCP call parses on the fly via tree-sitter +
# ripgrep, so there's nothing to index. record-index.sh still logs a row
# per repo (with build_seconds=0 and "(stateless)" placeholders) so the
# entrypoint banner shows the same shape across all tools.
COPY bench/global/docker/probe/CLAUDE.md /tmp/CLAUDE.md
COPY bench/global/docker/probe/mcp.json  /tmp/mcp.json
RUN set -eux && \
    for repo in axum discourse flask gin javalin nextjs; do \
      cp /tmp/CLAUDE.md /repos/$repo/CLAUDE.md && \
      cp /tmp/mcp.json  /repos/$repo/.mcp.json && \
      mkdir -p /repos/$repo/.claude && \
      printf '{"hooks":[]}\n' > /repos/$repo/.claude/settings.json && \
      /usr/local/lib/bench/record-index.sh probe "$repo" 0 ; \
    done && \
    rm /tmp/CLAUDE.md /tmp/mcp.json

ENV BENCH_TOOL=probe
# BENCH_TOOL_VERSION left unset — entrypoint emits null, matching host behavior
# (host scripts only fill it from `tools/probe.sh`'s TOOL_VERSION which is also
# computed at run time, not baked into the bench).
