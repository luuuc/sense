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

  # Clean-room: wipe prior config, write empty hooks to prevent ambient injection
  rm -rf "$workspace/.claude" "$workspace/CLAUDE.md" "$workspace/.mcp.json"
  mkdir -p "$workspace/.claude"
  echo '{"hooks":[]}' > "$workspace/.claude/settings.json"

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

No additional tools are configured. Use grep, find, Read, and Bash as needed.
EOF

  # Empty MCP config — no servers
  echo '{"mcpServers":{}}' > "$workspace/.mcp.json"
}

setup() {
  local repo="$1"
  local workspace="$2"

  echo "[$TOOL_NAME] Baseline — no MCP tools." >&2
  local start_time end_time
  start_time=$(date +%s)
  write_config "$@"
  end_time=$(date +%s)
  echo "{\"ready\": true, \"detail\": \"no tool\", \"setup_time_seconds\": $((end_time - start_time)), \"includes_embeddings\": false, \"deferred_embeddings\": false}" > "$workspace/index_meta_setup.json"
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
