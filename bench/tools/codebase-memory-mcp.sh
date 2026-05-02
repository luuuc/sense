#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="codebase-memory-mcp"
TOOL_VERSION="0.6.0"

usage() {
  echo "Usage: $0 [--check-ready|--write-config] <repo_path> <workspace_path>"
  exit 2
}

check_ready() {
  local repo="$1"
  local workspace="$2"

  if ! command -v codebase-memory-mcp &>/dev/null; then
    echo '{"ready": false, "detail": "codebase-memory-mcp binary not found"}'
    exit 2
  fi

  local repo_abs
  repo_abs=$(cd "$repo" && pwd)

  local status
  status=$(CBM_CACHE_DIR="$workspace/.cbm-cache" codebase-memory-mcp cli list_projects 2>/dev/null) || {
    echo '{"ready": false, "detail": "list_projects failed"}'
    exit 1
  }

  local has_repo
  has_repo=$(echo "$status" | python3 -c "
import sys, json
try:
    raw = json.loads(sys.stdin.read())
    # CLI wraps in MCP envelope: {content: [{text: '{...}'}]}
    inner = raw
    if 'content' in raw:
        inner = json.loads(raw['content'][0]['text'])
    projects = inner if isinstance(inner, list) else inner.get('projects', [])
    for p in projects:
        path = p.get('root_path', p.get('path', p.get('repo_path', '')))
        if path.rstrip('/') == sys.argv[1].rstrip('/'):
            print('true')
            sys.exit(0)
    print('false')
except Exception:
    print('false')
" "$repo_abs" 2>/dev/null) || has_repo="false"

  if [[ "$has_repo" == "true" ]]; then
    echo '{"ready": true, "detail": "repository indexed"}'
    exit 0
  else
    echo '{"ready": false, "detail": "repository not indexed"}'
    exit 1
  fi
}

write_config() {
  local repo="$1"
  local workspace="$2"
  local repo_abs
  repo_abs=$(cd "$repo" && pwd)

  # Clean-room: wipe prior config, write empty hooks to prevent ambient injection
  rm -rf "$workspace/.claude" "$workspace/CLAUDE.md" "$workspace/.mcp.json"
  mkdir -p "$workspace/.claude"
  echo '{"hooks":[]}' > "$workspace/.claude/settings.json"

  cat > "$workspace/.mcp.json" << EOF
{
  "mcpServers": {
    "codebase-memory-mcp": {
      "command": "codebase-memory-mcp",
      "args": [],
      "env": {
        "CBM_CACHE_DIR": "$workspace/.cbm-cache"
      }
    }
  }
}
EOF

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

codebase-memory-mcp provides MCP tools for codebase navigation:
- index_repository: index or re-index a codebase
- index_status: check indexing progress
- search_graph: filtered graph search (by node type, name, etc.)
- trace_call_path: directional call-chain tracing with configurable depth
- query_graph: Cypher-like queries for arbitrary graph traversals
- detect_changes: impact analysis for changed files
- get_graph_schema: see what's indexed (node counts, edge types)
- get_architecture: high-level architecture overview
- get_code_snippet: read source of specific functions/symbols
- search_code: text search across indexed code
- semantic_query: keyword-based search across the codebase graph
- list_projects: list all indexed projects
- delete_project: remove a project's index
- manage_adr: architecture decision record management
EOF
}

setup() {
  local repo="$1"
  local workspace="$2"

  echo "[$TOOL_NAME] Checking prerequisites..." >&2
  if ! command -v codebase-memory-mcp &>/dev/null; then
    echo "[$TOOL_NAME] ERROR: codebase-memory-mcp not found. Install: curl -fsSL https://raw.githubusercontent.com/DeusData/codebase-memory-mcp/main/install.sh | bash" >&2
    exit 2
  fi

  local version
  version=$(codebase-memory-mcp --version 2>/dev/null || echo "unknown")
  echo "[$TOOL_NAME] Using codebase-memory-mcp $version (pinned: $TOOL_VERSION)" >&2

  local repo_abs
  repo_abs=$(cd "$repo" && pwd)

  # Isolate cache to workspace so indexes don't leak between tools
  export CBM_CACHE_DIR="$workspace/.cbm-cache"
  mkdir -p "$CBM_CACHE_DIR"

  echo "[$TOOL_NAME] Indexing $repo..." >&2
  local start_time end_time
  start_time=$(date +%s)
  codebase-memory-mcp cli index_repository "{\"repo_path\": \"$repo_abs\"}"
  end_time=$(date +%s)

  echo "[$TOOL_NAME] Writing config to $workspace..." >&2
  write_config "$repo" "$workspace"

  # No neural embeddings — uses tree-sitter AST + SQLite FTS5 (BM25)
  echo "{\"setup_time_seconds\": $((end_time - start_time)), \"includes_embeddings\": false, \"deferred_embeddings\": false}" > "$workspace/index_meta_setup.json"

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
