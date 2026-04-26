#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="crg"
TOOL_VERSION="0.1.0"
VENV_NAME=".bench-crg-venv"

usage() {
  echo "Usage: $0 [--check-ready] <repo_path> <workspace_path>"
  exit 2
}

check_ready() {
  local repo="$1"
  local workspace="$2"
  local venv="$workspace/$VENV_NAME"

  if [[ ! -d "$venv" ]]; then
    echo '{"ready": false, "detail": "virtualenv not created"}'
    exit 1
  fi

  if [[ ! -d "$repo/.crg" ]]; then
    echo '{"ready": false, "detail": "CRG index directory not found"}'
    exit 1
  fi

  local file_count
  file_count=$(find "$repo/.crg" -type f | wc -l | tr -d ' ')
  if [[ "$file_count" -gt 0 ]]; then
    echo "{\"ready\": true, \"index_files\": $file_count, \"detail\": \"CRG index built ($file_count files)\"}"
    exit 0
  else
    echo '{"ready": false, "detail": ".crg directory exists but is empty — build may have failed"}'
    exit 1
  fi
}

setup() {
  local repo="$1"
  local workspace="$2"
  local venv="$workspace/$VENV_NAME"

  echo "[$TOOL_NAME] Checking prerequisites..." >&2

  if ! command -v python3 &>/dev/null; then
    echo "[$TOOL_NAME] ERROR: python3 not found." >&2
    exit 2
  fi

  local pyver
  pyver=$(python3 -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
  echo "[$TOOL_NAME] Python version: $pyver" >&2

  echo "[$TOOL_NAME] Creating virtualenv at $venv..." >&2
  python3 -m venv "$venv"
  source "$venv/bin/activate"

  echo "[$TOOL_NAME] Installing code-review-graph==$TOOL_VERSION..." >&2
  pip install --quiet "code-review-graph==$TOOL_VERSION"

  local version
  version=$(code-review-graph --version 2>/dev/null || echo "unknown")
  echo "[$TOOL_NAME] Using CRG $version (pinned: $TOOL_VERSION)" >&2

  echo "[$TOOL_NAME] Building graph index for $repo..." >&2
  (cd "$repo" && code-review-graph build)

  echo "[$TOOL_NAME] Writing MCP config to $workspace..." >&2
  local repo_abs
  repo_abs=$(cd "$repo" && pwd)

  cat > "$workspace/.mcp.json" << EOF
{
  "mcpServers": {
    "crg": {
      "command": "$venv/bin/code-review-graph",
      "args": ["serve", "--repo", "$repo_abs"]
    }
  }
}
EOF

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

Code Review Graph (CRG) provides MCP tools for structural code analysis:
- get_minimal_context_tool: focused subgraph context for a symbol
- get_impact_radius_tool: blast radius from changed files
- query_graph_tool: predefined graph queries (callers, callees, etc.)
- semantic_search_nodes_tool: keyword + vector search across nodes
- list_graph_stats_tool: aggregate index statistics
EOF

  deactivate
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
  ready) check_ready "$REPO" "$WORKSPACE" ;;
  setup) setup "$REPO" "$WORKSPACE" ;;
esac
