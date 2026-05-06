#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="sense"
TOOL_VERSION=""

usage() {
  echo "Usage: $0 [--check-ready|--write-config] <repo_path> <workspace_path>"
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
    "sense": {
      "command": "sense",
      "args": ["mcp", "--dir", "$repo_abs"]
    }
  }
}
EOF

  cat > "$workspace/CLAUDE.md" << 'EOF'
Use Sense MCP tools for structural code analysis. Read README.md for project overview. Read key source files when Sense results reference them.
Do not spawn Explore agents or sub-agents.

Sense provides four MCP tools:
- sense_graph: symbol relationships, callers, callees, inheritance
- sense_search: semantic code search by concept
- sense_blast: blast radius / impact analysis
- sense_conventions: project patterns and conventions
EOF
}

setup() {
  local repo="$1"
  local workspace="$2"

  echo "[$TOOL_NAME] Checking prerequisites..." >&2
  if ! command -v sense &>/dev/null; then
    echo "[$TOOL_NAME] ERROR: sense binary not found. Install: curl -fsSL https://sense.sh/install | sh" >&2
    exit 2
  fi

  TOOL_VERSION=$(sense --version 2>/dev/null || echo "dev")
  echo "[$TOOL_NAME] Using sense $TOOL_VERSION" >&2

  echo "[$TOOL_NAME] Indexing $repo..." >&2
  local start_time end_time
  start_time=$(date +%s)
  sense scan --dir "$repo" -embed
  end_time=$(date +%s)

  echo "[$TOOL_NAME] Writing config to $workspace..." >&2
  write_config "$repo" "$workspace"

  # sense scan -embed does parsing + embedding in one pass — no deferred work
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
  ready)        check_ready "$REPO" ;;
  write-config) write_config "$REPO" "$WORKSPACE" ;;
  setup)        setup "$REPO" "$WORKSPACE" ;;
esac
