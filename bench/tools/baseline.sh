#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="baseline"

usage() {
  echo "Usage: $0 [--check-ready|--write-config] <repo_path> <workspace_path>"
  exit 2
}

check_ready() {
  echo '{"ready": true, "detail": "no tool"}'
  exit 0
}

write_config() {
  local workspace="$2"

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

No additional tools are configured. Use grep, find, Read, and Bash as needed.
EOF
}

setup() {
  local workspace="$2"

  echo "[$TOOL_NAME] Baseline — no MCP tools." >&2
  write_config "$@"
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
  ready)        check_ready ;;
  write-config) write_config "$REPO" "$WORKSPACE" ;;
  setup)        setup "$REPO" "$WORKSPACE" ;;
esac
