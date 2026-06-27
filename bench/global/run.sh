#!/usr/bin/env bash
# run.sh — runs the bench inside per-tool docker images.
#
# Each tool has its own image (bench-sense, bench-gitnexus, bench-serena,
# bench-probe, bench-baseline) built from bench/global/docker/<tool>/Dockerfile.
# Every image is rooted at bench-baseline, which carries the claude CLI, the
# six reference repos at pinned commits, and bench/scenarios + bench/lib +
# bench/global/docker/entrypoint.sh. The tool layer adds the tool binary, runs
# its index/scan step, and writes per-repo Claude Code config so the
# scoring session boots with the tool's onboarding fully wired.
#
# Host mode (running setup scripts directly on the developer's machine)
# was removed on 2026-05-17 once docker became the canonical path. Every
# tool now ships through its image, so reproducibility doesn't depend on
# the host's PATH, ~/.claude, MCP servers, or shell hooks. The only
# host-side state we touch is the docker daemon and the output dir.

set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
# Resolves SCENARIOS_DIR + RESULTS_DIR for the global or VERTICAL bench.
source "$BENCH_DIR/lib/bench-paths.sh"
LIB_DIR="$BENCH_DIR/lib"
# shellcheck disable=SC1091
source "$LIB_DIR/load-env.sh"

# Per-session budgets/timeouts default to lib/scorer.py's per-repo numbers;
# both can be overridden globally with --budget / --timeout.
MAX_BUDGET_USD=""
SESSION_TIMEOUT=""

# --- Argument parsing ---

FILTER_TOOLS=""
FILTER_REPOS=""
DRY_RUN=false
NUM_RUNS=1
MODEL=""
DOCKER_TAG="dev"
DO_BUILD=false
# Recognised tools = per-tool docker image dirs under bench/global/docker/. The
# `lib/` directory holds shared shell+python helpers baked into every
# image (record-index.sh, parse-claude-result.py) and isn't a tool.
discover_tools() {
  local d
  for d in "$BENCH_DIR/global/docker"/*/; do
    [[ -d "$d" ]] || continue
    local name="$(basename "$d")"
    [[ "$name" == "lib" ]] && continue
    echo "$name"
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool)  FILTER_TOOLS="$2"; shift 2 ;;
    --repo)  FILTER_REPOS="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    --budget) MAX_BUDGET_USD="$2"; shift 2 ;;
    --timeout) SESSION_TIMEOUT="$2"; shift 2 ;;
    --runs) NUM_RUNS="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    --tag) DOCKER_TAG="$2"; shift 2 ;;
    --build) DO_BUILD=true; shift ;;
    -h|--help)
      cat <<EOF
Usage: run.sh [--tool t1,t2] [--repo r1,r2] [--runs N] [--model MODEL]
              [--dry-run] [--budget USD] [--timeout SECS] [--tag TAG]
              [--build]

Runs scenario-based evaluation: tool x scenario (repo). Each (tool, scenario)
pair runs inside bench-<tool>:<tag>, with /out volume-mounted to capture
transcript.json + run_meta.json + claude.log.

Options:
  --tool    Comma-separated tool filter (e.g. sense,baseline)
  --repo    Comma-separated repo filter (e.g. flask,discourse)
  --runs    Number of runs per scenario for variance estimation (default: 1)
  --model   Claude model to use (e.g. sonnet, opus)
  --dry-run Show what would run without executing claude
  --budget  Max USD per claude session (default: BUDGET_PER_REPO[repo] from lib/scorer.py)
  --timeout Max seconds per claude session (default: TIME_CEILINGS[repo])
  --tag     Image tag to run against (default: dev). Build bench-baseline:<TAG>
            first, then bench-<tool>:<TAG> for each non-baseline tool.
  --build   Run bench/global/build.sh with the same --tool/--tag filters before
            the scoring loop. Use this when you've changed Dockerfiles,
            tool source, or scenario YAMLs and want the images refreshed.
            Docker's layer cache makes a no-op rebuild cheap.
EOF
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

# --- Helpers ---

matches_filter() {
  local value="$1" filter="$2"
  [[ -z "$filter" ]] && return 0
  echo "$filter" | tr ',' '\n' | grep -qx "$value"
}

timestamp() { date +%Y-%m-%dT%H:%M:%S; }
log()       { echo "[$(timestamp)] $*" >&2; }

# Per-repo session budget/timeout default to lib/scorer.py constants.
# Bash 3.2 (macOS default) has no associative arrays; we shell out to
# python once per session — ~50ms overhead, negligible vs. claude time.
compute_session_budget() {
  local repo="$1"
  if [[ -n "$MAX_BUDGET_USD" ]]; then echo "$MAX_BUDGET_USD"; return; fi
  python3 -c "
import sys; sys.path.insert(0, '$LIB_DIR')
from scorer import BUDGET_PER_REPO, DEFAULT_BUDGET_USD
print(BUDGET_PER_REPO.get('$repo', DEFAULT_BUDGET_USD))
"
}
compute_session_timeout() {
  local repo="$1"
  if [[ -n "$SESSION_TIMEOUT" ]]; then echo "$SESSION_TIMEOUT"; return; fi
  python3 -c "
import sys; sys.path.insert(0, '$LIB_DIR')
from scorer import TIME_CEILINGS, DEFAULT_TIME_CEILING
print(max(300, TIME_CEILINGS.get('$repo', DEFAULT_TIME_CEILING)))
"
}

ensure_docker_image() {
  local tool="$1"
  local image="bench-${tool}:${DOCKER_TAG}"
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "ERROR: docker image $image not found locally." >&2
    echo "  Build it: docker build -f bench/global/docker/${tool}/Dockerfile -t $image ." >&2
    return 1
  fi
}

# --- Discover tools ---

tools=()
if [[ -n "$FILTER_TOOLS" ]]; then
  while IFS= read -r name; do
    [[ -d "$BENCH_DIR/global/docker/$name" ]] && tools+=("$name")
  done < <(echo "$FILTER_TOOLS" | tr ',' '\n')
else
  while IFS= read -r name; do
    tools+=("$name")
  done < <(discover_tools)
fi

# --- Discover scenarios ---

scenarios=()
scenario_files=()
for scenariofile in "$SCENARIOS_DIR"/*.yaml; do
  [[ -f "$scenariofile" ]] || continue
  name="$(basename "$scenariofile" .yaml)"
  repo=$(python3 -c "
import sys,yaml
d=yaml.safe_load(open(sys.argv[1]))
print(d['repo'])
" "$scenariofile" 2>/dev/null || echo "")
  [[ -z "$repo" ]] && continue
  matches_filter "$repo" "$FILTER_REPOS" || continue
  scenarios+=("$name")
  scenario_files+=("$scenariofile")
done

scenario_repo() {
  local name="$1"
  for i in "${!scenarios[@]}"; do
    if [[ "${scenarios[$i]}" == "$name" ]]; then
      python3 -c "
import yaml
print(yaml.safe_load(open('${scenario_files[$i]}'))['repo'])
"
      return
    fi
  done
  echo ""
}

if [[ ${#tools[@]} -eq 0 ]]; then
  echo "No tools found in bench/global/docker/ (excluding lib/)" >&2
  exit 1
fi
if [[ ${#scenarios[@]} -eq 0 ]]; then
  echo "No scenarios matched" >&2
  exit 1
fi

total_runs=$((${#tools[@]} * ${#scenarios[@]} * NUM_RUNS))
log "Evaluation: ${#tools[@]} tools x ${#scenarios[@]} scenarios = $total_runs runs (tag=$DOCKER_TAG)"
log "Tools: ${tools[*]}"
log "Scenarios: ${scenarios[*]}"

# --build: refresh images before running. Docker's layer cache makes a
# no-op rebuild cheap; this runs the same bench/global/build.sh the operator
# would run by hand, restricted to the same --tool/--tag scope as the
# scoring loop so a `--build --tool sense` doesn't rebuild every tool
# image. The build streams its own logs; ensure_docker_image after it
# is then just a sanity check.
if $DO_BUILD; then
  build_args=()
  [[ -n "$FILTER_TOOLS" ]] && build_args+=(--tool "$FILTER_TOOLS")
  build_args+=(--tag "$DOCKER_TAG")
  log "Running bench/global/build.sh ${build_args[*]} ..."
  bash "$BENCH_DIR/global/build.sh" "${build_args[@]}"
fi

# Image preflight — fail fast if any tool's image is missing locally.
for t in "${tools[@]}"; do
  ensure_docker_image "$t" || exit 1
done

if $DRY_RUN; then
  echo ""
  echo "=== DRY RUN (bench-<tool>:$DOCKER_TAG) ==="
  echo ""
  run_num=0
  for tool in "${tools[@]}"; do
    for scenario_name in "${scenarios[@]}"; do
      repo=$(scenario_repo "$scenario_name")
      for run_idx in $(seq 1 $NUM_RUNS); do
        run_num=$((run_num + 1))
        if [[ $NUM_RUNS -gt 1 ]]; then
          echo "  [$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name run=$run_idx/$NUM_RUNS"
        else
          echo "  [$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name"
        fi
      done
    done
  done
  echo ""
  echo "Estimated cost: ~\$$(echo "$total_runs * 0.10" | bc) (at ~\$0.10/session)"
  exit 0
fi

# --- Main loop ---

run_num=0
passed=0
failed=0

for tool in "${tools[@]}"; do
  for scenario_name in "${scenarios[@]}"; do
    repo=$(scenario_repo "$scenario_name")
    image="bench-${tool}:${DOCKER_TAG}"
    session_budget=$(compute_session_budget "$repo")
    session_timeout=$(compute_session_timeout "$repo")

    for run_idx in $(seq 1 $NUM_RUNS); do
      run_num=$((run_num + 1))
      if [[ $NUM_RUNS -gt 1 ]]; then
        log "[$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name run=$run_idx/$NUM_RUNS"
        result_dir="$RESULTS_DIR/$tool/$repo/run-$run_idx"
      else
        log "[$run_num/$total_runs] tool=$tool repo=$repo scenario=$scenario_name"
        result_dir="$RESULTS_DIR/$tool/$repo"
      fi
      mkdir -p "$result_dir"

      # bypassPermissions is root-gated by the claude CLI; containers run
      # as root by default. IS_SANDBOX=1 acknowledges we're in a sandbox
      # and lets the flag through.
      docker_args=(
        run --rm
        -e ANTHROPIC_API_KEY
        -e IS_SANDBOX=1
        -v "$result_dir:/out"
        "$image"
        --scenario "$scenario_name"
        --budget "$session_budget"
        --timeout "$session_timeout"
      )
      [[ -n "$MODEL" ]] && docker_args+=(--model "$MODEL")

      log "  docker run $image (budget=\$${session_budget} timeout=${session_timeout}s)..."
      # The entrypoint enforces its own timeout via `timeout` inside the
      # container. We don't wrap with a host-side timeout because the
      # container exits cleanly on session end and a hard kill would
      # leave /out half-written.
      docker "${docker_args[@]}" && claude_rc=0 || claude_rc=$?

      if [[ $claude_rc -eq 0 ]]; then
        log "  done"
        passed=$((passed + 1))
      else
        log "  FAIL: docker exit=$claude_rc"
        failed=$((failed + 1))
      fi
    done
  done
done

log ""
log "=== Evaluation complete ==="
log "  Total: $total_runs | Passed: $passed | Failed: $failed"
log "  Results in: $RESULTS_DIR/"
