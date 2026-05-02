#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="gitnexus"
TOOL_VERSION=""

usage() {
  echo "Usage: $0 [--check-ready|--write-config] <repo_path> <workspace_path>"
  exit 2
}

check_ready() {
  local repo="$1"

  if ! command -v gitnexus &>/dev/null && ! npx -y gitnexus@latest --version &>/dev/null 2>&1; then
    echo '{"ready": false, "detail": "gitnexus not found"}'
    exit 2
  fi

  if [[ ! -d "$repo/.gitnexus" ]]; then
    echo '{"ready": false, "detail": ".gitnexus directory not found"}'
    exit 1
  fi

  local file_count
  file_count=$(find "$repo/.gitnexus" -type f | wc -l | tr -d ' ')
  if [[ "$file_count" -gt 0 ]]; then
    echo "{\"ready\": true, \"index_files\": $file_count, \"detail\": \"gitnexus index exists ($file_count files)\"}"
    exit 0
  else
    echo '{"ready": false, "detail": ".gitnexus directory exists but is empty"}'
    exit 1
  fi
}

write_config() {
  local repo="$1"
  local workspace="$2"

  # Clean-room: wipe prior config, write empty hooks to prevent ambient injection
  rm -rf "$workspace/.claude" "$workspace/CLAUDE.md" "$workspace/.mcp.json"
  mkdir -p "$workspace/.claude"
  echo '{"hooks":[]}' > "$workspace/.claude/settings.json"

  # Determine command: prefer global install, fall back to npx
  local cmd="gitnexus"
  local args='["mcp"]'
  if ! command -v gitnexus &>/dev/null; then
    cmd="npx"
    args='["-y", "gitnexus@latest", "mcp"]'
  fi

  cat > "$workspace/.mcp.json" << EOF
{
  "mcpServers": {
    "gitnexus": {
      "command": "$cmd",
      "args": $args
    }
  }
}
EOF

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

GitNexus provides MCP tools for codebase navigation:
- list_repos: discover all indexed repositories
- query: hybrid BM25 + vector search over the knowledge graph
- cypher: raw Cypher queries against the graph schema
- context: 360-degree view of a symbol (callers, callees, refs)
- impact: blast radius / upstream+downstream with risk summary
- detect_changes: map git diffs to affected symbols
- rename: graph-assisted multi-file symbol rename (dry_run mode)
- route_map: API route -> handler -> consumer mappings
- tool_map: MCP/RPC tool definitions and handlers
- shape_check: response shape vs consumer property access mismatches
- api_impact: pre-change impact report for API route handlers
- group_list: list repo groups
- group_sync: rebuild group contract registry
EOF
}

setup() {
  local repo="$1"
  local workspace="$2"

  echo "[$TOOL_NAME] Checking prerequisites..." >&2

  # Prefer global install; fall back to npx
  if command -v gitnexus &>/dev/null; then
    TOOL_VERSION=$(gitnexus --version 2>/dev/null || echo "unknown")
  else
    echo "[$TOOL_NAME] No global install found, using npx..." >&2
    if ! npx -y gitnexus@latest --version &>/dev/null 2>&1; then
      echo "[$TOOL_NAME] ERROR: gitnexus not available. Install: npm install -g gitnexus" >&2
      exit 2
    fi
    TOOL_VERSION=$(npx -y gitnexus@latest --version 2>/dev/null || echo "unknown")
  fi
  echo "[$TOOL_NAME] Using gitnexus $TOOL_VERSION" >&2

  local repo_abs
  repo_abs=$(cd "$repo" && pwd)

  echo "[$TOOL_NAME] Indexing $repo (with embeddings)..." >&2
  local start_time end_time
  start_time=$(date +%s)
  if command -v gitnexus &>/dev/null; then
    (cd "$repo_abs" && gitnexus analyze --embeddings)
  else
    (cd "$repo_abs" && npx -y gitnexus@latest analyze --embeddings)
  fi
  end_time=$(date +%s)

  echo "[$TOOL_NAME] Writing config to $workspace..." >&2
  write_config "$repo" "$workspace"

  # GitNexus uses HuggingFace transformers.js for local embeddings
  echo "{\"setup_time_seconds\": $((end_time - start_time)), \"includes_embeddings\": true, \"deferred_embeddings\": false}" > "$workspace/index_meta_setup.json"

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
