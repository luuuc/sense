#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="grepai"
TOOL_VERSION="0.3.0"
OLLAMA_MODEL="nomic-embed-text"

usage() {
  echo "Usage: $0 [--check-ready] <repo_path> <workspace_path>"
  exit 2
}

check_ready() {
  local repo="$1"

  if ! command -v grepai &>/dev/null; then
    echo '{"ready": false, "detail": "grepai binary not found"}'
    exit 2
  fi

  if ! pgrep -x ollama &>/dev/null; then
    echo '{"ready": false, "detail": "Ollama is not running"}'
    exit 1
  fi

  local status
  if status=$(cd "$repo" && grepai status 2>/dev/null); then
    local indexed total
    indexed=$(echo "$status" | grep -oE '[0-9]+ indexed' | grep -oE '[0-9]+' || echo "0")
    total=$(echo "$status" | grep -oE '[0-9]+ total' | grep -oE '[0-9]+' || echo "0")

    if [[ "$indexed" -gt 0 && "$indexed" == "$total" ]]; then
      echo "{\"ready\": true, \"indexed\": $indexed, \"total\": $total, \"detail\": \"all files indexed\"}"
      exit 0
    else
      echo "{\"ready\": false, \"indexed\": $indexed, \"total\": $total, \"detail\": \"indexing in progress ($indexed/$total)\"}"
      exit 1
    fi
  else
    echo '{"ready": false, "detail": "grepai status failed — index may not exist"}'
    exit 1
  fi
}

setup() {
  local repo="$1"
  local workspace="$2"

  echo "[$TOOL_NAME] Checking prerequisites..." >&2

  if ! command -v grepai &>/dev/null; then
    echo "[$TOOL_NAME] ERROR: grepai not found. Install: brew install grepai" >&2
    exit 2
  fi

  if ! pgrep -x ollama &>/dev/null; then
    echo "[$TOOL_NAME] ERROR: Ollama is not running. Start it: ollama serve" >&2
    exit 2
  fi

  if ! ollama list 2>/dev/null | grep -q "$OLLAMA_MODEL"; then
    echo "[$TOOL_NAME] Pulling $OLLAMA_MODEL model (~270MB)..." >&2
    ollama pull "$OLLAMA_MODEL"
  fi

  local version
  version=$(grepai --version 2>/dev/null || echo "unknown")
  echo "[$TOOL_NAME] Using grepai $version (pinned: $TOOL_VERSION)" >&2

  echo "[$TOOL_NAME] Indexing $repo..." >&2
  (cd "$repo" && grepai init && grepai index)

  echo "[$TOOL_NAME] Writing MCP config to $workspace..." >&2
  local repo_abs
  repo_abs=$(cd "$repo" && pwd)

  cat > "$workspace/.mcp.json" << EOF
{
  "mcpServers": {
    "grepai": {
      "command": "grepai",
      "args": ["mcp-serve", "$repo_abs"]
    }
  }
}
EOF

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

grepai provides semantic code search and call graph analysis via MCP:
- grepai_search: semantic search across the codebase by natural language query
- grepai_trace_callers: find all functions that call a symbol
- grepai_trace_callees: find all functions called by a symbol
- grepai_trace_graph: build a call graph around a symbol
- grepai_index_status: check index health and statistics
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
