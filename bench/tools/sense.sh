#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="sense"
TOOL_VERSION="0.30.0"

usage() {
  echo "Usage: $0 [--check-ready] <repo_path> <workspace_path>"
  exit 2
}

check_ready() {
  local repo="$1"

  if ! command -v sense &>/dev/null; then
    echo '{"ready": false, "detail": "sense binary not found"}'
    exit 2
  fi

  local status
  status=$(cd "$repo" && sense status --json 2>/dev/null) || {
    echo '{"ready": false, "detail": "sense status failed"}'
    exit 1
  }

  local parsed
  parsed=$(echo "$status" | python3 -c "
import sys, json
d = json.load(sys.stdin)
s = d['freshness']['stale_files_seen']
f = d['index']['files']
sym = d['index']['symbols']
emb = d['index']['embeddings']
ready = s == 0 and emb == sym and sym > 0
detail = 'all passes complete' if ready else f'index incomplete (stale={s}, embeddings={emb}/{sym})'
out = {'ready': ready, 'files': f, 'symbols': sym, 'embeddings': emb, 'detail': detail}
if not ready:
    out['stale'] = s
print(json.dumps(out))
" 2>/dev/null) || {
    echo '{"ready": false, "detail": "failed to parse sense status JSON"}'
    exit 1
  }

  echo "$parsed"
  if echo "$parsed" | python3 -c "import sys,json; sys.exit(0 if json.load(sys.stdin)['ready'] else 1)"; then
    exit 0
  else
    exit 1
  fi
}

setup() {
  local repo="$1"
  local workspace="$2"

  echo "[$TOOL_NAME] Checking prerequisites..." >&2
  if ! command -v sense &>/dev/null; then
    echo "[$TOOL_NAME] ERROR: sense binary not found. Install: curl -fsSL https://sense.sh/install | sh" >&2
    exit 2
  fi

  local version
  version=$(sense --version 2>/dev/null || echo "unknown")
  echo "[$TOOL_NAME] Using sense $version (pinned: $TOOL_VERSION)" >&2

  echo "[$TOOL_NAME] Indexing $repo..." >&2
  sense scan --dir "$repo"

  echo "[$TOOL_NAME] Writing MCP config to $workspace..." >&2
  local repo_abs
  repo_abs=$(cd "$repo" && pwd)

  cat > "$workspace/.mcp.json" << EOF
{
  "mcpServers": {
    "sense": {
      "command": "sense",
      "args": ["mcp", "--dir", "$repo_abs"]
    }
  }
}
EOF

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

Sense provides four MCP tools:
- sense_graph: symbol relationships, callers, callees, inheritance
- sense_search: semantic code search by concept
- sense_blast: blast radius / impact analysis
- sense_conventions: project patterns and conventions
EOF

  echo "[$TOOL_NAME] Setup complete." >&2
}

# --- Main ---

MODE="setup"
if [[ "${1:-}" == "--check-ready" ]]; then
  MODE="ready"
  shift
fi

[[ $# -ge 2 ]] || usage

REPO="$1"
WORKSPACE="$2"

case "$MODE" in
  ready) check_ready "$REPO" ;;
  setup) setup "$REPO" "$WORKSPACE" ;;
esac
