# Tool Script Protocol

Every `tools/*.sh` script must implement two modes: **setup** and **ready**.

## Modes

### Setup mode (default)

```bash
tools/sense.sh <repo_path> <workspace_path>
```

Install the tool (if needed), run initial indexing against `<repo_path>`, write MCP config and CLAUDE.md to `<workspace_path>`. May return before indexing is fully complete — some tools index asynchronously or in multiple passes.

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

## Runner contract

The runner (`run.sh`) calls each tool script as follows:

```bash
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
    # Tool is broken, not just building — no point polling
    break
  fi
  sleep 5
done

# 3. Capture index state as metadata
tools/$tool.sh --check-ready "$repo" "$workspace" > "$results_dir/index_meta.json"

if [[ "$ready" != "true" ]]; then
  # Record failure and skip this run
  echo '{"index_completeness": "timeout_or_failed"}' > "$results_dir/transcript.json"
  continue
fi

# 4. Run Claude session (only after ready)
claude -p "$prompt" --output-format json --cwd "$workspace" > "$results_dir/transcript.json"
```

Exit code semantics: **0** = ready (break, proceed to Claude), **1** = still building (keep polling), **2** = broken/unavailable (break, skip run). If the tool never reaches exit 0 within the timeout, the run is skipped and recorded as failed.

## Per-tool readiness signals

| Tool | Ready when | How to check |
|---|---|---|
| **sense** | `sense status` shows 0 stale files, embeddings = symbols | `sense status --json` |
| **grepai** | `grepai status` shows all files indexed, Ollama running | `grepai status` exit code |
| **crg** | `code-review-graph build` exited 0 | Check for index file existence |
| **tokensave** | `tokensave init` exited 0 | Check for `.tokensave/` directory |
| **roam** | `roam init` exited 0, optional semantic index if `[semantic]` extras | Check for `.roam/` directory |
| **cbm** | `cbm index` exited 0 or first-query auto-index complete | Check for index file |
| **baseline** | Always ready (no tool) | Exit 0 immediately |

## Baseline special case

The baseline script has no index. Its `--check-ready` always returns `{"ready": true, "detail": "no tool"}` and exits 0.

## Runner responsibilities

The following concerns are the runner's job, not the tool scripts':

- **Virtualenv caching.** Python-based tools (CRG, roam) create virtualenvs in the workspace. If the runner creates a fresh temp workspace per run, pip packages get reinstalled every time. For a full matrix (196 runs, ~56 pip installs), the runner should maintain a shared venv cache directory outside the workspace and pass it to the tool scripts, or reuse workspaces across tasks for the same tool × repo combination.

- **Output routing.** Setup mode writes progress to stderr. `--check-ready` writes exactly one JSON line to stdout. The runner must capture stdout separately from stderr to parse readiness JSON reliably.

- **Index time exclusion.** Wall time should measure only the Claude session, not setup or indexing. The runner starts the timer after `--check-ready` returns 0, not when setup begins.
