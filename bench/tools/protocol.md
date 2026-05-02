# Tool Script Protocol

Every `tools/*.sh` script must implement three modes: **setup**, **ready**, and **write-config**.

## Modes

### Setup mode (default)

```bash
tools/sense.sh <repo_path> <workspace_path>
```

Install the tool (if needed), run initial indexing against `<repo_path>`, write MCP config and CLAUDE.md to `<workspace_path>`. Must also write `index_meta_setup.json` to `<workspace_path>` with scan timing metadata (see below).

### Ready mode

```bash
tools/sense.sh --check-ready <repo_path> <workspace_path>
```

Check whether the tool's index is fully built and usable. Exits with:

- **0** — index is complete, all features operational
- **1** — index is still building, not yet ready
- **2** — index failed or tool is unavailable

Prints a JSON status line to stdout:

```json
{"ready": true, "files": 255, "symbols": 1966, "embeddings": 1966, "detail": "all passes complete"}
```

```json
{"ready": false, "files": 255, "symbols": 1966, "embeddings": 400, "detail": "embedding pass in progress (400/1966)"}
```

The JSON must include at least `ready` (bool). Other fields are tool-specific and get recorded as `index_completeness` metadata in the results.

### Write-config mode

```bash
tools/sense.sh --write-config <repo_path> <workspace_path>
```

Write `.mcp.json` and `CLAUDE.md` to `<workspace_path>` without indexing. Used when the index already exists and only the Claude session config needs refreshing.

This mode must also set up clean-room isolation:
- Remove any prior `$workspace/.claude/`, `$workspace/CLAUDE.md`, `$workspace/.mcp.json`
- Write `{"hooks":[]}` to `$workspace/.claude/settings.json` to prevent ambient hook injection
- Write `.mcp.json` pointing to the tool's MCP server
- Write `CLAUDE.md` listing available MCP tools

## Setup output: `index_meta_setup.json`

Setup mode must write `index_meta_setup.json` to `<workspace_path>` with scan timing metadata:

```json
{
  "setup_time_seconds": 42,
  "includes_embeddings": true,
  "deferred_embeddings": false
}
```

| Field | Type | Description |
|---|---|---|
| `setup_time_seconds` | int | Wall-clock seconds for the indexing step |
| `includes_embeddings` | bool | Whether the tool generates vector embeddings |
| `deferred_embeddings` | bool | Whether embeddings are computed in a background process after setup returns |

The runner copies this file to the results directory. The reporter uses it for scan-time comparison tables.

## Runner contract

The runner (`run.sh`) calls each tool script as follows:

```bash
# 0. (Optional) Reset indexes for fresh scan timing
if $RESET; then
  rm -rf "$repo/.sense" "$repo/.roam" "$repo/.code-review-graph" "$repo/.grepai" "$repo/.tokensave"
  rm -rf "$workspace"
  mkdir -p "$workspace"
fi

# 1. Setup (install + start indexing)
tools/$tool.sh "$repo" "$workspace"

# 2. Poll until ready (max 10 minutes, 5s intervals)
# Exit 0 = ready, exit 1 = still building (keep polling), exit 2 = broken (stop)
ready=false
for i in $(seq 1 120); do
  tools/$tool.sh --check-ready "$repo" "$workspace" > /dev/null
  rc=$?
  if [[ $rc -eq 0 ]]; then
    ready=true
    break
  elif [[ $rc -eq 2 ]]; then
    break
  fi
  sleep 5
done

# 3. Capture index state as metadata
tools/$tool.sh --check-ready "$repo" "$workspace" > "$results_dir/index_meta.json"

# 4. Copy scan timing metadata
cp "$workspace/index_meta_setup.json" "$results_dir/index_meta_setup.json"

if [[ "$ready" != "true" ]]; then
  echo '{"index_completeness": "timeout_or_failed"}' > "$results_dir/transcript.json"
  continue
fi

# 5. Run Claude session (only after ready)
claude -p "$prompt" --output-format json --cwd "$workspace" > "$results_dir/transcript.json"
```

Exit code semantics: **0** = ready (break, proceed to Claude), **1** = still building (keep polling), **2** = broken/unavailable (break, skip run). If the tool never reaches exit 0 within the timeout, the run is skipped and recorded as failed.

## Per-tool readiness signals

| Tool | Ready when | How to check |
|---|---|---|
| **sense** | `sense status` shows 0 stale files, embeddings = symbols | `sense status --json` |
| **grepai** | `grepai status` shows all files indexed, Ollama running | `grepai status` exit code |
| **codebase-memory-mcp** | `index_status` shows the repo indexed | `codebase-memory-mcp cli index_status` |
| **gitnexus** | `gitnexus analyze` exited 0 | Check for `.gitnexus/` directory |
| **tokensave** | `tokensave init` exited 0 | Check for `.tokensave/` directory |
| **roam** | `roam init` exited 0, optional semantic index if `[semantic]` extras | Check for `.roam/` directory |
| **baseline** | Always ready (no tool) | Exit 0 immediately |

## Baseline special case

The baseline script has no index. Its `--check-ready` always returns `{"ready": true, "detail": "no tool"}` and exits 0. Its `index_meta_setup.json` reports `setup_time_seconds: 0` with no embeddings.

## Runner responsibilities

The following concerns are the runner's job, not the tool scripts':

- **Virtualenv caching.** Python-based tools (CRG, roam) create virtualenvs in the workspace. The runner maintains persistent workspaces at `results/<tool>/<repo>/.workspace/` so pip packages are only installed once per tool × repo pair.

- **Output routing.** Setup mode writes progress to stderr. `--check-ready` writes exactly one JSON line to stdout. The runner must capture stdout separately from stderr to parse readiness JSON reliably.

- **Index time exclusion.** Wall time should measure only the Claude session, not setup or indexing. The runner starts the timer after `--check-ready` returns 0, not when setup begins. Scan time is captured separately in `index_meta_setup.json`.

- **Index reset.** The `--reset` flag deletes tool-specific index directories and workspaces before setup, forcing a full re-index. This is required to capture accurate scan timing.
