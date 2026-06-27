# syntax=docker/dockerfile:1.4
# bench-serena: serena MCP server + pre-built .serena/cache/ per repo.
#
# Build from the repo root:
#   docker build -f bench/global/docker/serena/Dockerfile -t bench-serena:dev .
#
# Serena's per-repo CLAUDE.md .mcp.json contains an absolute --project path,
# so each repo gets its own mcp.json generated in-place. The .serena/cache/
# is populated by `serena project index` at build time.

# Global build args (declared before any FROM so they're visible in every stage).
ARG BASE_TAG=dev

# ── Stage 1: Ruby 3.4 source for /opt/ruby34 ───────────────────────────────
# Debian bookworm ships Ruby 3.1, but discourse's Gemfile requires "ruby ~> 3.4"
# and ruby-lsp runs `bundle install` against the project which then refuses on
# version mismatch. Carrying Ruby 3.4 alongside Debian's interpreter (in /opt,
# not /usr/local, to avoid colliding with rust-analyzer / go we install later).
FROM ruby:3.4-slim-bookworm AS ruby34

# ── Stage 2: serena runtime ────────────────────────────────────────────────
FROM bench-baseline:${BASE_TAG}

# uv + python 3.13 + serena. The `serena-agent` package ships pre-release
# wheels; --prerelease=allow is required.
#
# /root/.cache/uv survives rebuilds — uv downloads Python 3.13 and serena's
# dependency wheels once, reuses them on every subsequent build. Material
# when bumping serena versions during dev.
RUN --mount=type=cache,target=/root/.cache/uv,sharing=locked \
    curl -fsSL https://astral.sh/uv/install.sh | bash \
 && /root/.local/bin/uv tool install -p 3.13 serena-agent@latest --prerelease=allow \
 && /root/.local/bin/serena --version

# rust-analyzer for the axum repo. Serena auto-downloads LSPs for Python/JS/Go/
# Kotlin/Ruby but expects rust-analyzer on PATH. Pin to a release binary —
# building from source would add multi-GB of toolchain.
RUN ARCH=$(uname -m) \
 && case "$ARCH" in \
      aarch64) RA_ARCH=aarch64-unknown-linux-gnu ;; \
      x86_64)  RA_ARCH=x86_64-unknown-linux-gnu ;; \
      *) echo "unsupported arch: $ARCH" >&2; exit 1 ;; \
    esac \
 && curl -fsSL "https://github.com/rust-lang/rust-analyzer/releases/latest/download/rust-analyzer-${RA_ARCH}.gz" \
      | gunzip > /usr/local/bin/rust-analyzer \
 && chmod +x /usr/local/bin/rust-analyzer \
 && rust-analyzer --version

# `serena project index` is interactive on first run (asks Y/N for each minor
# language detected, 1-5% file share). `yes n` declines all minor languages
# to keep the index focused on the dominant language. Dockerfile RUN runs
# under /bin/sh (dash); pipefail is off by default there so SIGPIPE on `yes`
# is ignored without further plumbing.
RUN t0=$(date +%s); echo "[serena] indexing /repos/axum ..."; \
    cd /repos/axum && yes n 2>/dev/null | serena project index && \
    /usr/local/lib/bench/record-index.sh serena axum $(( $(date +%s) - t0 ))

# Language toolchains needed by the remaining repos' serena indexing passes.
# Each language server links against its host toolchain at runtime: solargraph
# imports Ruby gems (discourse), gopls calls `go` (gin), jdtls needs a JDK
# (javalin/Kotlin), typescript-language-server runs on node (nextjs).
#
# Ruby 3.4 is copied into /usr/local directly. The ruby image's gem/bundle
# binstubs have a hardcoded `#!/usr/local/bin/ruby` shebang; relocating Ruby
# would also require rewriting every shebang. /usr/local/lib is on the default
# dynamic linker path so libruby.so.3.4 resolves without LD_LIBRARY_PATH.
COPY --from=ruby34 /usr/local /usr/local

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get update && apt-get install -y --no-install-recommends \
      openjdk-17-jdk-headless \
      build-essential \
      unzip xz-utils \
      libyaml-dev \
 && ARCH=$(uname -m) \
 && case "$ARCH" in \
      aarch64) GO_ARCH=arm64 ;; \
      x86_64)  GO_ARCH=amd64 ;; \
    esac \
 && curl -fsSL "https://go.dev/dl/go1.25.5.linux-${GO_ARCH}.tar.gz" -o /tmp/go.tgz \
 && tar -C /usr/local -xzf /tmp/go.tgz \
 && rm /tmp/go.tgz \
 && ln -s /usr/local/go/bin/go /usr/local/bin/go \
 && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt \
 && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && ruby --version && go version && java --version && node --version

# Per-language LSP servers. ruby-lsp must be installed under Ruby 3.4 (the
# /opt/ruby34 interpreter on PATH); discourse's Gemfile demands ~> 3.4 and
# ruby-lsp's bundler check refuses to run otherwise.
ARG KOTLIN_LSP_VERSION=1.3.13
# Cache gem, npm, and go module downloads. ruby-lsp + typescript-language-server
# install hundreds of transitive deps — those don't need to be re-fetched
# every time the parent layer invalidates.
RUN --mount=type=cache,target=/root/.npm,sharing=locked \
    --mount=type=cache,target=/root/.cache/gem,sharing=locked \
    --mount=type=cache,target=/root/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    gem install --no-document ruby-lsp \
 && npm install -g typescript typescript-language-server \
 && GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@latest \
 && curl -fsSL "https://github.com/fwcd/kotlin-language-server/releases/download/${KOTLIN_LSP_VERSION}/server.zip" -o /tmp/kls.zip \
 && unzip -q /tmp/kls.zip -d /opt/ \
 && ln -s /opt/server/bin/kotlin-language-server /usr/local/bin/kotlin-language-server \
 && rm /tmp/kls.zip \
 && ruby-lsp --version && typescript-language-server --version && gopls version \
 && test -x /opt/server/bin/kotlin-language-server

# Discourse: ruby-lsp's bundler integration resolves the full Gemfile.lock,
# so every gem must be installable. Native deps require a long apt list.
# Once `bundle install` succeeds inside /repos/discourse, ruby-lsp finds the
# gems on $GEM_PATH and serena's symbol indexer can proceed.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    --mount=type=cache,target=/usr/local/bundle/cache,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get update && apt-get install -y --no-install-recommends \
      libpq-dev libxml2-dev libxslt-dev libssl-dev libreadline-dev \
      libffi-dev zlib1g-dev libcurl4-openssl-dev libsasl2-dev \
      libgmp-dev libjemalloc-dev libmagickwand-dev libheif-dev \
      libwebp-dev libidn2-dev libsqlite3-dev libimlib2-dev \
      pkg-config cmake automake autoconf libtool \
 && cd /repos/discourse \
 && bundle config set --local jobs 4 \
 && bundle config set --local retry 2 \
 && bundle install \
 && apt-get purge -y \
      pkg-config cmake automake autoconf libtool

RUN t0=$(date +%s); echo "[serena] indexing /repos/discourse ..."; \
    cd /repos/discourse && yes n 2>/dev/null | serena project index && \
    /usr/local/lib/bench/record-index.sh serena discourse $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[serena] indexing /repos/flask ..."; \
    cd /repos/flask && yes n 2>/dev/null | serena project index && \
    /usr/local/lib/bench/record-index.sh serena flask $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[serena] indexing /repos/gin ..."; \
    cd /repos/gin && yes n 2>/dev/null | serena project index && \
    /usr/local/lib/bench/record-index.sh serena gin $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[serena] indexing /repos/javalin ..."; \
    cd /repos/javalin && yes n 2>/dev/null | serena project index && \
    /usr/local/lib/bench/record-index.sh serena javalin $(( $(date +%s) - t0 ))
RUN t0=$(date +%s); echo "[serena] indexing /repos/nextjs ..."; \
    cd /repos/nextjs && yes n 2>/dev/null | serena project index && \
    /usr/local/lib/bench/record-index.sh serena nextjs $(( $(date +%s) - t0 ))

# Claude Code integration for serena. Two pieces:
#
#   1. `serena setup claude-code` runs ONCE (not per-repo): it registers
#      serena as a USER-scope MCP server in /root/.claude/.claude.json
#      with `--project-from-cwd`, so any session that cd's into a repo
#      under /repos/ resolves the right project automatically. Running
#      it a second time errors with "MCP server serena already exists
#      in user config" — that's the upstream-supplied idempotency
#      story, and we honour it.
#
#   2. Per-repo .mcp.json is written explicitly with an absolute
#      --project path. This is belt-and-suspenders: it shadows the
#      user-scope registration with a deterministic project binding,
#      so the bench doesn't depend on claude-code's MCP merge order.
#      Also overrides bench-baseline's empty `.mcp.json`.
#
# Hooks: serena's docs recommend activate/remind/auto-approve/cleanup
# hooks ("alpha but recommended"). Their exact JSON shape isn't
# documented in a form we can script-generate, so for now we leave
# .claude/settings.json at the baseline's empty-hooks default. The
# system-prompt override + onboarding pre-run below capture serena's
# two highest-impact upstream recommendations regardless.
RUN cd /repos/axum && serena setup claude-code
RUN set -eux && \
    for repo in axum discourse flask gin javalin nextjs; do \
      printf '{\n  "mcpServers": {\n    "serena": {\n      "command": "serena",\n      "args": ["start-mcp-server", "--context", "claude-code", "--project", "/repos/%s", "--open-web-dashboard", "false"]\n    }\n  }\n}\n' "$repo" > /repos/$repo/.mcp.json ; \
    done

# Cache the system-prompt override that serena ships. Per upstream docs,
# Claude Code's built-in tool descriptions occupy ~16k tokens and create
# a "very strong bias towards internal tools, making it almost impossible
# to convince Opus 4.7 to use Serena". The override neutralises that bias.
# entrypoint.sh appends --system-prompt "$(cat /bench/serena-system-prompt.txt)"
# to the claude invocation when BENCH_TOOL=serena.
RUN serena prompts print-cc-system-prompt-override > /bench/serena-system-prompt.txt \
 && test -s /bench/serena-system-prompt.txt

# Pre-run mcp__serena__onboarding once per repo. The onboarding flow asks
# the LLM to explore the codebase and persist findings to .serena/memories/
# via write_memory; the scoring agent then reads those memories instead of
# repeating the exploration on its own dime.
#
# Fairness: onboarding burns real LLM tokens/cost/time. To avoid hiding
# this overhead from the comparison, each onboarding session's `result`
# event (cost_usd, input_tokens, output_tokens, duration_ms) is parsed
# into /bench/onboarding-overhead/<repo>.json. entrypoint.sh copies the
# right file into /out, and scorer.py folds the overhead into the
# scoring session's metrics so serena's efficiency reflects the full
# cost of using it. Per-repo cost is ~$0.15-0.30, paid once at image
# build (not per scoring run).
#
# Fails open: without the anthropic_key secret, memories stay empty
# (scoring agent will onboard inline) and overhead JSON is omitted
# (scorer.py treats absent file as zero overhead — still correct, the
# inline-onboarding cost is then captured by the scoring transcript itself).
#
# Build with:
#   docker build --secret id=anthropic_key,env=ANTHROPIC_API_KEY \
#                -f bench/global/docker/serena/Dockerfile -t bench-serena:dev .
# (Requires BuildKit, which is the Docker default since 23.0.)
RUN --mount=type=secret,id=anthropic_key \
    set -eu; \
    mkdir -p /bench/onboarding-overhead; \
    if [ -f /run/secrets/anthropic_key ]; then \
      export ANTHROPIC_API_KEY="$(cat /run/secrets/anthropic_key)"; \
      # claude refuses --permission-mode bypassPermissions under root
      # ("for security reasons") unless IS_SANDBOX=1 is set. The bench
      # runtime sets this via `docker run -e IS_SANDBOX=1`; at build
      # time we set it inline.
      export IS_SANDBOX=1; \
      SYS_OVERRIDE="$(cat /bench/serena-system-prompt.txt)"; \
      for repo in axum discourse flask gin javalin nextjs; do \
        echo "[serena] onboarding /repos/$repo ..."; \
        log="/tmp/onboarding-$repo.log"; \
        t0=$(date +%s); \
        (cd /repos/$repo && timeout 300 claude \
            -p "Use the mcp__serena__onboarding tool to perform project onboarding. Then use mcp__serena__write_memory to persist 3-5 short notes about the project (overview, key directories, entrypoints, conventions). Reply DONE and exit. Do not explore beyond what onboarding requires." \
            --verbose \
            --system-prompt "$SYS_OVERRIDE" \
            --output-format stream-json \
            --max-turns 25 \
            --max-budget-usd 0.50 \
            --permission-mode bypassPermissions \
            --disallowed-tools Agent \
            > "$log" 2>&1) \
          || echo "[serena] onboarding for $repo finished with non-zero rc — see $log"; \
        t1=$(date +%s); \
        /usr/local/lib/bench/parse-claude-result.py \
          "$log" "/bench/onboarding-overhead/$repo.json" "$((t1-t0))" "$repo"; \
      done; \
      echo "[serena] onboarding done; memories under /repos/<repo>/.serena/memories/"; \
    else \
      echo "[serena] WARNING: no anthropic_key build secret — skipping onboarding."; \
      echo "[serena] To bake memories + capture overhead, build with: --secret id=anthropic_key,env=ANTHROPIC_API_KEY"; \
    fi

ENV BENCH_TOOL=serena
