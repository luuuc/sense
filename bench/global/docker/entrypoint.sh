#!/usr/bin/env bash
# Shared entrypoint baked into bench-baseline (the foundation image
# every tool layer FROMs).
#
# Contract:
#   docker run --rm -e ANTHROPIC_API_KEY -v <host_out>:/out \
#     bench-<tool>:<tag> --scenario <name> [--model M] [--budget USD] [--timeout S]
#
# Writes /out/transcript.json + /out/run_meta.json + /out/claude.log.
# Mirrors the inner block of bench/run.sh:423-515 so scoring code is unchanged.
set -euo pipefail

SCENARIO=""
MODEL=""
BUDGET=""
TIMEOUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario) SCENARIO="$2"; shift 2 ;;
    --model)    MODEL="$2"; shift 2 ;;
    --budget)   BUDGET="$2"; shift 2 ;;
    --timeout)  TIMEOUT="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: docker run bench-<tool>:<tag> --scenario <name> [--model M] [--budget USD] [--timeout S]"
      exit 0
      ;;
    *) echo "Unknown arg: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$SCENARIO" ]] || { echo "--scenario required" >&2; exit 2; }
[[ -n "${ANTHROPIC_API_KEY:-}" ]] || { echo "ANTHROPIC_API_KEY required (pass with -e)" >&2; exit 2; }

SCEN_FILE="/bench/scenarios/${SCENARIO}.yaml"
[[ -f "$SCEN_FILE" ]] || { echo "scenario not found: $SCEN_FILE" >&2; exit 2; }

REPO=$(python3 -c "import yaml; print(yaml.safe_load(open('$SCEN_FILE'))['repo'])")
REPO_DIR="/repos/$REPO"
[[ -d "$REPO_DIR" ]] || { echo "repo not found: $REPO_DIR" >&2; exit 2; }

# Defaults from bench/lib/scorer.py — identical resolution to run.sh's
# compute_session_budget / compute_session_timeout.
if [[ -z "$BUDGET" ]]; then
  BUDGET=$(python3 -c "
import sys; sys.path.insert(0, '/bench/lib')
from scorer import BUDGET_PER_REPO, DEFAULT_BUDGET_USD
print(BUDGET_PER_REPO.get('$REPO', DEFAULT_BUDGET_USD))
")
fi
if [[ -z "$TIMEOUT" ]]; then
  TIMEOUT=$(python3 -c "
import sys; sys.path.insert(0, '/bench/lib')
from scorer import TIME_CEILINGS, DEFAULT_TIME_CEILING
print(max(300, TIME_CEILINGS.get('$REPO', DEFAULT_TIME_CEILING)))
")
fi

PROMPT=$(python3 /bench/lib/scenario.py "$SCEN_FILE" --prompt)

mkdir -p /out
TOOL="${BENCH_TOOL:-unknown}"
TOOL_VERSION="${BENCH_TOOL_VERSION:-}"
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)
START=$(date +%s)

# Print the (tool, repo) index banner before launching claude so the
# operator sees what was indexed, when, and the persistence model. Stats
# come from /bench/index-info.tsv, baked at `docker build` time by each
# tool's Dockerfile via /usr/local/lib/bench/record-index.sh. The index
# is immutable across container runs — to refresh it, rebuild the image.
if [[ -f /bench/index-info.tsv ]]; then
  echo "─── bench-${TOOL} index info (persistent in image; rebuild image to refresh) ───" >&2
  head -1 /bench/index-info.tsv | column -t -s $'\t' >&2 || head -1 /bench/index-info.tsv >&2
  awk -F'\t' -v t="$TOOL" -v r="$REPO" \
      'NR>1 && $1==t && $2==r' /bench/index-info.tsv \
    | column -t -s $'\t' >&2 || true
  echo "────────────────────────────────────────────────────────────" >&2
fi

CLAUDE_ARGS=(
  -p "$PROMPT"
  --verbose
  --output-format stream-json
  --permission-mode bypassPermissions
  --disallowed-tools "Agent"
  --max-budget-usd "$BUDGET"
)
[[ -n "$MODEL" ]] && CLAUDE_ARGS+=(--model "$MODEL")

# Per-tool last-mile flags. Each upstream has its own recommended way of
# being launched inside Claude Code; the bench reflects those recs here
# rather than smoothing them into a single one-size-fits-all invocation.
case "$TOOL" in
  serena)
    # Serena ships a CC-specific system-prompt override that's documented
    # as essential — without it, CC's built-in tool descriptions (~16k
    # tokens) bias the model away from MCP tools. The override is baked
    # into the image by the serena Dockerfile and read here at run time.
    if [[ -f /bench/serena-system-prompt.txt ]]; then
      CLAUDE_ARGS+=(--system-prompt "$(cat /bench/serena-system-prompt.txt)")
    fi
    # Surface the build-time onboarding overhead for this repo so scorer.py
    # can fold it into serena's metrics — onboarding burns real tokens/cost
    # before the scoring session even starts, and hiding that would inflate
    # serena's efficiency vs. tools that need no LLM onboarding step.
    if [[ -f "/bench/onboarding-overhead/$REPO.json" ]]; then
      cp "/bench/onboarding-overhead/$REPO.json" /out/onboarding-overhead.json
    fi
    ;;
esac

cd "$REPO_DIR"
set +e
timeout "$TIMEOUT" claude "${CLAUDE_ARGS[@]}" > /out/transcript.json 2> /out/claude.log
RC=$?
set -e
END=$(date +%s)
WALL=$((END - START))

REPO_COMMIT=$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo "")

python3 - "$TOOL" "$REPO" "$SCENARIO" "$WALL" "$BUDGET" "$TIMESTAMP" \
            "$TOOL_VERSION" "$REPO_COMMIT" "$MODEL" "$RC" > /out/run_meta.json <<'PY'
import json, sys
tool, repo, scen, wall, budget, ts, ver, commit, model, rc = sys.argv[1:11]
meta = {
    "tool": tool, "repo": repo, "scenario": scen,
    "wall_time_seconds": int(wall),
    "max_budget_usd": float(budget),
    "timestamp": ts,
    "tool_version": ver or None,
    "repo_commit": commit or None,
    "model": model or None,
    "claude_exit_code": int(rc),
}
if int(rc) != 0:
    meta["error"] = "claude_session_failed"
print(json.dumps(meta, indent=2))
PY

exit "$RC"
