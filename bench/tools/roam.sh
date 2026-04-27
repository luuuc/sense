#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="roam"
TOOL_VERSION="11.0.0"
VENV_NAME=".bench-roam-venv"

usage() {
  echo "Usage: $0 [--check-ready|--write-config] <repo_path> <workspace_path>"
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

  if [[ ! -d "$repo/.roam" ]]; then
    echo '{"ready": false, "detail": ".roam directory not found"}'
    exit 1
  fi

  local file_count
  file_count=$(find "$repo/.roam" -type f | wc -l | tr -d ' ')
  if [[ "$file_count" -gt 0 ]]; then
    echo "{\"ready\": true, \"index_files\": $file_count, \"detail\": \"roam index exists ($file_count files)\"}"
    exit 0
  else
    echo '{"ready": false, "detail": ".roam directory exists but is empty — init may have failed"}'
    exit 1
  fi
}

write_config() {
  local repo="$1"
  local workspace="$2"
  local venv="$workspace/$VENV_NAME"

  cat > "$workspace/.mcp.json" << EOF
{
  "mcpServers": {
    "roam-code": {
      "command": "$venv/bin/roam",
      "args": ["mcp"]
    }
  }
}
EOF

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

roam-code provides MCP tools for codebase navigation:
- roam_search_symbol: find symbols by name
- roam_context: get relevant context for a symbol or file
- roam_trace: call graph traversal (callers and callees)
- roam_impact: impact analysis for a symbol change
- roam_dead_code: find unreachable symbols
- roam_explore: explore codebase structure
- roam_deps: dependency analysis
EOF
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

  # tree-sitter language packs may lack wheels for bleeding-edge Python;
  # prefer 3.13 when available, fall back to system python3.
  local py="python3"
  for candidate in python3.13 python3.12; do
    if command -v "$candidate" &>/dev/null; then py="$candidate"; break; fi
  done
  echo "[$TOOL_NAME] Creating virtualenv at $venv (using $py)..." >&2
  "$py" -m venv "$venv"
  source "$venv/bin/activate"

  echo "[$TOOL_NAME] Installing roam-code[mcp,semantic]==$TOOL_VERSION..." >&2
  pip install --quiet "roam-code[mcp,semantic]==$TOOL_VERSION"

  local version
  version=$(roam --version 2>/dev/null || echo "unknown")
  echo "[$TOOL_NAME] Using roam $version (pinned: $TOOL_VERSION)" >&2

  echo "[$TOOL_NAME] Indexing $repo..." >&2
  (cd "$repo" && roam init)

  echo "[$TOOL_NAME] Writing config to $workspace..." >&2
  write_config "$repo" "$workspace"

  deactivate
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
  ready)        check_ready "$REPO" "$WORKSPACE" ;;
  write-config) write_config "$REPO" "$WORKSPACE" ;;
  setup)        setup "$REPO" "$WORKSPACE" ;;
esac
