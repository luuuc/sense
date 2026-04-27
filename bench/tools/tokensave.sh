#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="tokensave"
TOOL_VERSION="4.1.4"

usage() {
  echo "Usage: $0 [--check-ready|--write-config] <repo_path> <workspace_path>"
  exit 2
}

check_ready() {
  local repo="$1"

  if ! command -v tokensave &>/dev/null; then
    echo '{"ready": false, "detail": "tokensave binary not found"}'
    exit 2
  fi

  if [[ ! -d "$repo/.tokensave" ]]; then
    echo '{"ready": false, "detail": ".tokensave directory not found"}'
    exit 1
  fi

  local file_count
  file_count=$(find "$repo/.tokensave" -type f | wc -l | tr -d ' ')
  if [[ "$file_count" -gt 0 ]]; then
    echo "{\"ready\": true, \"index_files\": $file_count, \"detail\": \"tokensave index exists ($file_count files)\"}"
    exit 0
  else
    echo '{"ready": false, "detail": ".tokensave directory exists but is empty"}'
    exit 1
  fi
}

write_config() {
  local workspace="$2"

  cat > "$workspace/.mcp.json" << EOF
{
  "mcpServers": {
    "tokensave": {
      "command": "tokensave",
      "args": ["serve"]
    }
  }
}
EOF

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

tokensave provides MCP tools for token-efficient codebase queries:
- tokensave_search: find symbols by name
- tokensave_context: get relevant code context for a task
- tokensave_callers: find what calls a function
- tokensave_callees: find what a function calls
- tokensave_impact: what's affected by changing a symbol
- tokensave_dead_code: find unreachable symbols
- tokensave_node: get details + source code for a specific symbol
EOF
}

setup() {
  local repo="$1"
  local workspace="$2"

  echo "[$TOOL_NAME] Checking prerequisites..." >&2

  if ! command -v tokensave &>/dev/null; then
    echo "[$TOOL_NAME] ERROR: tokensave not found. Install: brew install aovestdipaperino/tap/tokensave" >&2
    exit 2
  fi

  local version
  version=$(tokensave --version 2>/dev/null || echo "unknown")
  echo "[$TOOL_NAME] Using tokensave $version (pinned: $TOOL_VERSION)" >&2

  echo "[$TOOL_NAME] Indexing $repo..." >&2
  (cd "$repo" && yes | tokensave init) || (cd "$repo" && tokensave sync)

  echo "[$TOOL_NAME] Writing config to $workspace..." >&2
  write_config "$repo" "$workspace"

  echo "[$TOOL_NAME] Setup complete." >&2
}

# --- Main ---

MODE="setup"
case "${1:-}" in
  --check-ready)  MODE="ready"; shift ;;
  --write-config) MODE="write-config"; shift ;;
esac

[[ $# -ge 2 ]] || usage

REPO="$1"
WORKSPACE="$2"

case "$MODE" in
  ready)        check_ready "$REPO" ;;
  write-config) write_config "$REPO" "$WORKSPACE" ;;
  setup)        setup "$REPO" "$WORKSPACE" ;;
esac
