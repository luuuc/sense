# Competitive Evaluation Harness

Runs the same codebase-understanding tasks with Sense, competitors, and a bare baseline, then measures what actually happens: correctness, token usage, tool calls, misses, and wall time.

## What it measures

| Metric | Best | Description |
|--------|------|-------------|
| **Correctness** | Higher | F1 (precision/recall) for structural tasks, keyword presence for qualitative tasks |
| **Token usage** | Lower | Input + output tokens consumed per session |
| **Token savings** | Higher | Reduction vs. baseline (no tool) |
| **Tool calls** | Lower | Total calls — fewer means more efficient |
| **Misses** | Lower | Times Claude had an MCP tool but used grep/Read/Agent instead |
| **Wall time** | Lower | Seconds from prompt to final answer |
| **Scan time** | Lower | Seconds to index a repo (measured once per tool per repo) |
| **Cost** | Lower | USD spent on Claude API per session |

Misses are the novel metric. A tool with zero misses has perfect discoverability — Claude always reaches for it. A tool with frequent misses has a discoverability problem regardless of accuracy.

## Tasks

| Task | Type | What it tests |
|------|------|---------------|
| **callers** | set_match (F1) | Find all callers of a symbol. Tests structural code navigation. |
| **blast-radius** | set_match (F1) | What breaks if a symbol changes. Tests impact analysis. |
| **dead-code** | set_match (F1) | Find unused symbols. Tests reachability analysis. |
| **semantic-search** | set_match (F1) | Find code by concept. Tests semantic understanding. |
| **grep-task** | set_match (F1) | Find exact text matches. Tests raw search (grep baseline). |
| **data-flow** | set_match (F1) | Trace data from entry to storage. Tests architectural tracing. |
| **test-file** | set_match (F1) | Find the test file for a source file. Tests convention awareness. |
| **orient** | qualitative | Orient in an unfamiliar codebase. Tests high-level understanding. |
| **conventions** | qualitative | Identify patterns in a code domain. Tests architectural understanding. |
| **refactor** | qualitative | Assess risks before refactoring. Tests dependency awareness. |

## Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- Python 3.9+ (for task parsing and scoring)
- `bc` (for cost estimates in dry-run)

```bash
python3 -m venv bench/.venv
source bench/.venv/bin/activate
pip install -r bench/requirements.txt
```

Activate the venv (`source bench/.venv/bin/activate`) in each new shell before running bench scripts.

### Per-tool dependencies

| Tool | Version | Install | Extra dependencies |
|------|---------|---------|-------------------|
| sense | (dynamic) | `curl -fsSL https://sense.sh/install \| sh` | None |
| grepai | 0.35.0 | `brew install grepai` | Ollama + `nomic-embed-text` model running |
| codebase-memory-mcp | 0.6.0 | `curl -fsSL https://raw.githubusercontent.com/DeusData/codebase-memory-mcp/main/install.sh \| bash` | None |
| gitnexus | (latest) | `npm install -g gitnexus` | Node.js 18+ |
| tokensave | 4.3.2 | `brew install aovestdipaperino/tap/tokensave` | None |
| roam | 12.2.0 | Installed to venv by script | Python 3.9+ |
| baseline | — | — | None |

Python tools (roam) get dedicated virtualenvs created by their setup scripts — no system install needed.

## Quick start: clean run

This is the full workflow from clone to scored report. Each step is idempotent — safe to re-run.

```bash
# 1. Activate the bench venv
source bench/.venv/bin/activate

# 2. Clone repos (skip any you already have)
cd bench/repos
git clone --depth=1 https://github.com/pallets/flask.git flask
git clone --depth=1 https://github.com/discourse/discourse.git discourse
git clone --depth=1 https://github.com/opf/openproject.git openproject
git clone --depth=1 https://github.com/gin-gonic/gin.git gin
git clone --depth=1 https://github.com/maket-store/maket-web.git maket
git clone --depth=1 https://github.com/vercel/next.js.git nextjs
git clone --depth=1 https://github.com/javalin/javalin.git javalin
git clone --depth=1 https://github.com/tokio-rs/axum.git axum
cd ..

# 3. Pin repos to known commits (optional — repos/PINNED_COMMITS.json tracks these)
# The runner warns if a repo is at a different commit than pinned.

# 4. Generate ground truth from scratch (for set_match tasks)
bash bench/gen-ground-truth.sh

# 5. Dry run to see the full matrix
bash bench/run.sh --dry-run

# 6. Run with fresh indexes and N=3 for variance
bash bench/run.sh --reset --runs 3

# 7. Score all transcripts
bash bench/score.sh

# 8. Generate report (also auto-runs after scoring)
bash bench/report.sh --md
```

### Partial run (subset of tools/repos/tasks)

```bash
# Single tool + repo + task
bash bench/run.sh --tool sense --repo flask --task callers

# Compare two tools across all tasks
bash bench/run.sh --tool sense,baseline --repo flask

# Multiple filters
bash bench/run.sh --tool sense,grepai,baseline --repo flask,gin --task callers,blast-radius
```

## Script reference

### `run.sh` — Run Claude sessions

```
Usage: run.sh [--tool t1,t2] [--repo r1,r2] [--task t1,t2] [--runs N]
              [--dry-run] [--reset] [--verify-isolation] [--budget USD] [--timeout SECS]

Options:
  --tool    Comma-separated tool filter (e.g. sense,baseline)
  --repo    Comma-separated repo filter (e.g. flask,gin)
  --task    Comma-separated task filter (e.g. callers,blast-radius)
  --runs    Number of runs per combination for variance estimation (default: 1)
  --dry-run Show what would run without executing Claude sessions
  --reset   Delete existing indexes and workspaces to measure fresh scan time
  --verify-isolation  Scan existing transcripts for Sense MCP contamination
  --budget  Max USD per Claude session (default: 1.00)
  --timeout Max seconds per Claude session (default: 600)
```

The `--reset` flag deletes tool-specific index directories (`.sense/`, `.roam/`, `.gitnexus/`, `.grepai/`, `.tokensave/`, `.cbm-cache/`) and workspaces, forcing a full re-index. This is required to capture accurate scan timing in `index_meta_setup.json`.

The `--runs N` flag runs each tool/repo/task combination N times, storing results in `run-1/`, `run-2/`, etc. subdirectories. Use N=3 or N=5 for statistical significance.

### `score.sh` — Score transcripts

```
Usage: score.sh [--tool t1,t2] [--repo r1,r2] [--task t1,t2]
```

Scores each transcript against ground truth. Writes `scored.json` next to each `transcript.json`. Automatically regenerates `results/report.md` after scoring.

### `report.sh` — Generate report

```
Usage: report.sh [--format terminal|markdown|json] [--json] [--md]
```

Generates comparison tables. The markdown report includes: metric legend, per-task tables ranked by score, token savings section, efficiency section, per-task best-tool rankings, aggregate, and global ranking.

### `gen-ground-truth.sh` — Generate ground truth

```
Usage: gen-ground-truth.sh [--repo r1,r2] [--task t1,t2]
```

Generates ground truth from grep/static analysis (not from Sense) to avoid circular validation bias. Supports callers, blast-radius, and dead-code tasks. Semantic-search and qualitative tasks require manual curation.

### `setup.sh` — Pre-index repos

```
Usage: setup.sh [--tool t1,t2] [--repo r1,r2]
```

Indexes all tool x repo pairs. Equivalent to running `run.sh` but without Claude sessions. Useful for pre-warming indexes before a timed run.

## Results directory

```
results/
  <tool>/
    <repo>/
      .workspace/           — Persistent workspace (venvs, configs)
      <task>/
        transcript.json     — Claude session output (stream-json JSONL)
        scored.json         — Scored metrics and correctness
        run_meta.json       — Wall time, tool version, repo commit, timestamp
        index_meta.json     — Tool index state at run time
        index_meta_setup.json — Scan/index timing and embedding info
        setup.log           — Tool setup stderr
        claude.log          — Claude session stderr
        run-1/              — Multi-run subdirectory (when --runs N > 1)
        run-2/
  report.md                 — Markdown comparison table
  report.json               — Machine-readable report
```

## Adding a task

Create `tasks/<name>.yaml`:

```yaml
name: my-task
description: What this task tests
variables: [symbol]
prompt_template: |
  Find all callers of `{symbol}`. Respond with JSON: { "callers": [...] }

repos:
  flask:
    symbol: "Flask.route"
    ground_truth_file: ground-truth/flask/my-task.json
  gin:
    symbol: "Engine.ServeHTTP"
    ground_truth_file: ground-truth/gin/my-task.json

scoring:
  correctness:
    type: set_match       # or: qualitative
    match_key: callers    # JSON key to compare
    partial_credit: true
  metrics:
    - tool_calls
    - token_input
    - token_output
    - wall_time
    - misses
    - index_completeness
```

Create matching ground-truth JSON files with a `status` field (`verified`, `initial`, or `stub`).

## Adding a tool

Create `tools/<name>.sh` implementing three modes per [tools/protocol.md](tools/protocol.md):

1. **Setup**: `tools/<name>.sh <repo_path> <workspace_path>` — install, index, write `.mcp.json` and `CLAUDE.md` to workspace
2. **Ready**: `tools/<name>.sh --check-ready <repo_path> <workspace_path>` — exit 0 (ready), 1 (building), or 2 (broken)
3. **Write config**: `tools/<name>.sh --write-config <repo_path> <workspace_path>` — write `.mcp.json` and `CLAUDE.md` only (no indexing)

Add the tool's capabilities to `TOOL_CAPABILITIES` in `lib/scorer.py` for miss detection.

## Scoring types

**`set_match`** — For structural tasks (callers, blast-radius, dead-code, semantic-search, grep-task, data-flow, test-file). Compares response JSON array against ground-truth set. Computes precision, recall, and F1. Supports partial credit for `file:symbol` matches.

**`qualitative`** (`keyword_presence`) — For conceptual tasks (orient, conventions, refactor). Checks which ground-truth keywords appear in the response text via exact substring match or word-proximity matching (all significant words within a 200-char window). Score = fraction found.

## Ground truth

Ground-truth files have three tiers:

- **`verified`** — Generated from independent sources (grep, static analysis) on pinned repo commits. High confidence.
- **`initial`** — Derived from tool output or manual inspection. Medium confidence — may carry circular bias.
- **`stub`** — Empty placeholder. Scoring is skipped.

Use `gen-ground-truth.sh` to generate `verified` ground truth from grep/static analysis for set_match tasks. Qualitative tasks (orient, conventions, refactor) require manual keyword curation.

## Candidate tools for future evaluation

| Tool | Stars | Language | MCP tools | License | Install | Notes |
|------|------:|----------|----------:|---------|---------|-------|
| **graphify** | 39.5k | Python | 7 | MIT | `pip install graphifyy` | Multimodal knowledge graph (code + docs + video). Leiden community detection, HTML visualization. |
| **gortex** | 24 | Go | 50 | PolyForm SB | `curl -fsSL https://get.gortex.dev \| sh` | 92 languages, hybrid BM25+vector search, LSP-enriched graphs. Global cache dir. |
| **CodeGraphContext** | 3.1k | Python | native | MIT | `pip install codegraphcontext` | Graph-DB backend (KuzuDB). Different architecture from tree-sitter-only tools. |
| **mcp-language-server** | 1.5k | Go | LSP bridge | BSD-3 | Go binary | Wraps any LSP server (gopls, pyright, rust-analyzer) via MCP. Compiler-grade accuracy. |
| **Repowise** | 1.3k | Python | 7 | AGPL | `pip install repowise` | Multi-layer: graph + git history + docs. Unique `get_why()` decision archaeology. |
| **jCodeMunch** | 1.7k | Python | native | — | pip | Token-efficient symbol retrieval. Direct tokensave competitor. |
