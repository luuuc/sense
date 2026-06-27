#!/usr/bin/env bash
# build.sh — build all bench-* docker images in dependency order.
#
# bench-baseline is the foundation: it carries the claude CLI, the 6
# reference repos at pinned SHAs, bench/scenarios + bench/lib +
# entrypoint.sh + record-index.sh, plus the no-MCP per-repo config. Every
# tool image (sense, serena, gitnexus, probe) does `FROM bench-baseline`
# and overrides BENCH_TOOL + per-repo files. So baseline must complete
# first; the tool images then build in parallel against the shared
# foundation layer.
#
# Docker's layer cache makes incremental rebuilds cheap — only layers
# downstream of an actual change re-run. First-ever build is slow
# (~20-30min: clone 6 repos, scan/index each tool's 6 repos, install
# LSPs in serena). With cache-mount support (`# syntax=docker/dockerfile:1.4`
# at the top of each Dockerfile + BuildKit-enabled docker daemon), apt /
# npm / uv / go module caches also survive across rebuilds.
#
# Usage:
#   bash bench/global/build.sh                 # build all five
#   bash bench/global/build.sh --tool sense    # build one
#   bash bench/global/build.sh --no-cache      # force full rebuild
#   bash bench/global/build.sh --serial        # one at a time, full output
#   bash bench/global/build.sh --tag v0.84.3   # custom tag
#
# When $ANTHROPIC_API_KEY is set in the environment, it's automatically
# forwarded to bench-serena as a build secret so the onboarding pre-run
# can fire (memories baked into the image). Without it, serena builds
# fine but with empty .serena/memories/ — onboarding then happens at
# scoring time inline. See bench/global/docker/serena/Dockerfile.

set -euo pipefail
# `-o pipefail` (already set above by -euo) propagates the rightmost
# non-zero exit code through internal pipes. The OUTER pipe of any
# caller (e.g. `bash bench/global/build.sh | tee /tmp/log`) is the caller's
# responsibility — they need to either `set -o pipefail` themselves or
# check ${PIPESTATUS[0]}. Below, on any failure path we also write an
# explicit `[BUILD FAILED]` marker so a grep against the log always
# tells the truth regardless of how the caller piped it.

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
# shellcheck disable=SC1091
source "$BENCH_DIR/lib/load-env.sh"

TAG="dev"
FILTER_TOOLS=""
NO_CACHE=false
# Serial is the safe default. Four parallel docker builds × discourse's
# heavy steps (sense's `scan -embed`, serena's `bundle install`, gitnexus's
# `analyze --embeddings`) all loading native LLM/embedding models and
# compiling tree-sitter grammars at the same time can OOM a typical Docker
# Desktop allocation (4-8 GB). --parallel is opt-in for hosts with enough
# RAM (16 GB+ given to Docker).
PARALLEL=false
LOG_DIR="${TMPDIR:-/tmp}/bench-build-$(date +%s)"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool) FILTER_TOOLS="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --no-cache) NO_CACHE=true; shift ;;
    --serial) PARALLEL=false; shift ;;
    --parallel) PARALLEL=true; shift ;;
    -h|--help)
      cat <<EOF
Usage: build.sh [--tool t1,t2] [--tag TAG] [--no-cache] [--parallel|--serial]

Builds bench-* docker images in dependency order: bench-baseline first,
then sense/serena/gitnexus/probe.

Options:
  --tool t1,t2  Comma-separated subset to build (baseline is always built
                first if its dependents are in the set, or if it itself
                is in the set; otherwise it's only re-checked).
  --tag TAG     Image tag to produce (default: dev).
  --no-cache    Force docker to rebuild every layer ignoring the cache.
  --serial      Build one tool at a time with full streaming output.
                This is the default — safer on hosts where Docker Desktop
                has ≤8 GB; the discourse-heavy index/install steps can
                OOM if four tools build in parallel.
  --parallel    Build the four tool images concurrently. Faster on hosts
                with ≥16 GB given to Docker. Logs go to per-tool files
                under ${TMPDIR:-/tmp}/bench-build-<epoch>/.

When ANTHROPIC_API_KEY is set in env, bench-serena gets it as a build
secret so onboarding bakes memories into the image.
EOF
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

mkdir -p "$LOG_DIR"
cd "$PROJECT_ROOT"

timestamp() { date +%H:%M:%S; }
log() { echo "[$(timestamp)] $*" >&2; }

# matches_filter "$tool" → 0 if tool is in $FILTER_TOOLS (or filter empty), 1 otherwise.
matches_filter() {
  local tool="$1"
  [[ -z "$FILTER_TOOLS" ]] && return 0
  echo "$FILTER_TOOLS" | tr ',' '\n' | grep -qx "$tool"
}

# Populate the global DOCKER_BUILD_ARGS array with the args for `docker`
# to build bench-<tool>:<TAG>. Using a global rather than echoing back
# through command-substitution so the caller can background `docker`
# directly — backgrounding inside $(...) creates a subshell-child that
# the parent shell can't `wait` for.
DOCKER_BUILD_ARGS=()
build_args_for_tool() {
  local tool="$1"
  local image="bench-${tool}:${TAG}"
  local dockerfile="bench/global/docker/${tool}/Dockerfile"

  if [[ ! -f "$dockerfile" ]]; then
    echo "ERROR: $dockerfile not found" >&2
    return 1
  fi

  DOCKER_BUILD_ARGS=(build --progress plain -f "$dockerfile" -t "$image"
                     --build-arg "BASE_TAG=${TAG}")
  $NO_CACHE && DOCKER_BUILD_ARGS+=(--no-cache)

  # Serena's onboarding step reads /run/secrets/anthropic_key at build
  # time. Skipping the secret leaves memories empty (scoring-time inline
  # onboarding takes over) — surfaced as a one-line warning, not a fail.
  if [[ "$tool" == "serena" && -n "${ANTHROPIC_API_KEY:-}" ]]; then
    DOCKER_BUILD_ARGS+=(--secret id=anthropic_key,env=ANTHROPIC_API_KEY)
  elif [[ "$tool" == "serena" ]]; then
    log "  bench-serena: ANTHROPIC_API_KEY not set — building without onboarding secret"
    log "  (image still builds; serena memories will be empty until first scoring run)"
  fi

  DOCKER_BUILD_ARGS+=(.)
}

# Foreground (serial) build — streams docker output to the terminal.
build_image_serial() {
  local tool="$1"
  local image="bench-${tool}:${TAG}"
  build_args_for_tool "$tool" || return 1

  local t0=$(date +%s)
  log "── building $image ─────────────────────────────────────────"
  if docker "${DOCKER_BUILD_ARGS[@]}"; then
    local t1=$(date +%s)
    log "── $image done in $((t1-t0))s ─────────────────────────────"
    return 0
  else
    log "── $image FAILED ───────────────────────────────────────────"
    return 1
  fi
}

# Wait on a backgrounded docker build, print outcome + duration.
wait_build() {
  local tool="$1" pid="$2" t0="$3"
  local log_file="$LOG_DIR/${tool}.log"
  if wait "$pid"; then
    local t1=$(date +%s)
    log "✓ bench-${tool}:${TAG} done in $((t1-t0))s (log: $log_file)"
    return 0
  else
    log "✗ bench-${tool}:${TAG} FAILED (log: $log_file)"
    log "  Last 30 lines:"
    tail -30 "$log_file" >&2
    return 1
  fi
}

# Step 1: foundation. Always required if anything else is being built;
# rebuilding is cheap on cache-hit, so we always re-invoke it (Docker
# will short-circuit if nothing changed).
if $PARALLEL; then
  log "═══ build sequence: baseline → {sense, serena, gitnexus, probe} in parallel ═══"
  log "logs: $LOG_DIR/"
else
  log "═══ build sequence: baseline → sense → serena → gitnexus → probe (serial) ═══"
fi

die_failed() {
  local tool="$1"
  log "═══ [BUILD FAILED] tool=$tool ═══"
  log "  log: ${LOG_DIR}/${tool}.log (parallel mode) or stdout above (serial)"
  exit 1
}

if matches_filter baseline || [[ -z "$FILTER_TOOLS" ]] \
   || matches_filter sense || matches_filter serena \
   || matches_filter gitnexus || matches_filter probe; then
  # Build baseline serially (full output to terminal) — it's the foundation;
  # if it fails everything else fails, and there's no parallelism to gain.
  build_image_serial baseline || die_failed baseline
fi

# Stage host-built sense indexes (currently just nextjs) into the docker
# build context before bench-sense builds. See build-prescan.sh for the
# why — sense scan on nextjs in-container OOMs even with 16 GB; host has
# the RAM, so we do it there once and ship the .sense in.
if matches_filter sense || [[ -z "$FILTER_TOOLS" ]]; then
  log "── staging host-built sense indexes (build-prescan.sh) ─────"
  bash "$BENCH_DIR/global/build-prescan.sh" || die_failed "sense-prescan"
fi

# Step 2: tool images in parallel against the freshly-cached baseline.
tool_list=(sense serena gitnexus probe)
to_build=()
for t in "${tool_list[@]}"; do
  matches_filter "$t" && to_build+=("$t")
done

if [[ ${#to_build[@]} -eq 0 ]]; then
  log "Nothing else to build."
else
  if $PARALLEL; then
    # macOS ships bash 3.2 which lacks `declare -A`. Track pids and
    # start_times in two ordinary arrays indexed positionally — the
    # i-th entry in pids/start_times corresponds to to_build[i].
    #
    # Crucially, the `docker ... &` runs in THIS shell (not a $(...)
    # subshell), so `$!` and `wait $pid` work the way they should.
    pids=()
    start_times=()
    for t in "${to_build[@]}"; do
      build_args_for_tool "$t" || exit 1
      start_times+=("$(date +%s)")
      log "→ launching bench-${t}:${TAG} (log: $LOG_DIR/${t}.log)"
      docker "${DOCKER_BUILD_ARGS[@]}" >"$LOG_DIR/${t}.log" 2>&1 &
      pids+=("$!")
    done
    failed=0
    failed_tools=()
    for i in "${!to_build[@]}"; do
      if ! wait_build "${to_build[$i]}" "${pids[$i]}" "${start_times[$i]}"; then
        failed=1
        failed_tools+=("${to_build[$i]}")
      fi
    done
    if [[ $failed -eq 1 ]]; then
      log "═══ [BUILD FAILED] tools=${failed_tools[*]} ═══"
      exit 1
    fi
  else
    for t in "${to_build[@]}"; do
      build_image_serial "$t" || die_failed "$t"
    done
  fi
fi

log ""
log "═══ [BUILD OK] all requested images present ═══"
docker images --format 'table {{.Repository}}\t{{.Tag}}\t{{.Size}}' \
  | awk -v tag="$TAG" 'NR==1 || ($1 ~ /^bench-/ && $2==tag)' >&2
