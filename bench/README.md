# Competitive Evaluation Harness

Runs the same codebase-understanding tasks with Sense, 4 competitors, and a bare baseline, then measures what actually happens: correctness, token usage, tool calls, misses, and wall time.

## What it measures

| Metric | Description |
|--------|-------------|
| **Correctness** | F1 (precision/recall) for structural tasks, keyword presence for qualitative tasks |
| **Token usage** | Input + output tokens consumed per session |
| **Token savings** | Reduction vs. baseline (no tool) |
| **Tool calls** | Total calls, MCP vs. built-in breakdown |
| **Misses** | Times Claude had an MCP tool but reached for grep/Read/Agent instead |
| **Wall time** | Seconds from prompt to final answer |

Misses are the novel metric. A tool with zero misses has perfect discoverability — Claude always reaches for it. A tool with frequent misses has a discoverability problem regardless of accuracy.

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

| Tool | Install | Extra dependencies |
|------|---------|-------------------|
| sense | `curl -fsSL https://sense.sh/install \| sh` | None |
| grepai | `brew install grepai` | Ollama + `nomic-embed-text` model running |
| crg | `pip install code-review-graph` (installed to venv by script) | Python 3.10+ |
| tokensave | `brew install aovestdipaperino/tap/tokensave` | None |
| roam | `pip install "roam-code[mcp,semantic]"` (installed to venv by script) | Python 3.9+ |
| baseline | — | None |

Python tools (crg, roam) get dedicated virtualenvs created by their setup scripts — no system install needed.

## Setup repos

Clone benchmark repos into `bench/repos/`. See [repos/README.md](repos/README.md) for exact clone commands and pinning instructions.

```bash
cd bench/repos
git clone https://github.com/pallets/flask.git flask
git clone https://github.com/discourse/discourse.git discourse
git clone https://github.com/opf/openproject.git openproject
git clone https://github.com/gin-gonic/gin.git gin
git clone https://github.com/vercel/next.js.git nextjs
```

Pin each repo to a specific commit for reproducible ground-truth (see repos/README.md).

## Setup

Index repos before running evaluations. Indexes persist in repo directories (`.sense/`, `.roam/`, `.code-review-graph/`, `.grepai/`, `.tokensave/`) and only need to be built once.

```bash
# Index all tools × all repos
bash bench/setup.sh

# Index specific tools/repos
bash bench/setup.sh --tool sense,roam --repo flask,gin
```

Already-indexed tool+repo pairs are skipped automatically.

## Running

### Full matrix

```bash
bash bench/run.sh
```

Runs all 6 tools × 5 repos × 7 tasks = 210 Claude sessions. Estimated cost: ~$10.50 at ~$0.05/session.

If a tool is already indexed (from `setup.sh` or a prior run), `run.sh` skips setup and writes only the lightweight workspace config. If not indexed, it runs full setup inline.

### Partial runs

```bash
# Single tool + repo + task
bash bench/run.sh --tool sense --repo flask --task callers

# Compare sense vs baseline on all tasks
bash bench/run.sh --tool sense,baseline --repo flask

# Multiple filters (comma-separated)
bash bench/run.sh --tool sense,grepai,baseline --repo flask,discourse --task callers,blast-radius
```

### Dry run

```bash
bash bench/run.sh --dry-run
```

Shows what would execute without running Claude sessions. Reports which repos are present/missing and estimated cost.

### Budget and timeout control

```bash
bash bench/run.sh --budget 2.00    # max $2.00 per Claude session (default: $1.00)
bash bench/run.sh --timeout 900    # max 900s per Claude session (default: 600)
```

## Scoring

After a run completes:

```bash
bash bench/score.sh
```

Scores each transcript against ground truth. Writes `scored.json` next to each `transcript.json` in `results/<tool>/<repo>/<task>/`.

Supports the same `--tool`, `--repo`, `--task` filters as `run.sh`.

## Reporting

```bash
# Terminal table
bash bench/report.sh

# Markdown (also writes results/report.md)
bash bench/report.sh --md

# JSON (also writes results/report.json)
bash bench/report.sh --json
```

## Results directory

```
results/
  <tool>/
    <repo>/
      <task>/
        transcript.json   — Claude session output (stream-json JSONL)
        scored.json       — Scored metrics and correctness
        run_meta.json     — Wall time, tool version, repo commit, timestamp
        index_meta.json   — Tool index readiness at run time
        setup.log         — Tool setup stderr
        claude.log        — Claude session stderr
        workspace_debug/  — Preserved workspace on failure (for debugging)
  report.md               — Markdown comparison table
  report.json             — Machine-readable report
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
  discourse:
    symbol: "TopicCreator#create"
    ground_truth_file: ground-truth/discourse/my-task.json

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
3. **Write config**: `tools/<name>.sh --write-config <repo_path> <workspace_path>` — write `.mcp.json` and `CLAUDE.md` only (no indexing). Used by `run.sh` when the tool is already indexed

Add the tool's capabilities to `TOOL_CAPABILITIES` in `lib/scorer.py` for miss detection.

## Scoring types

**`set_match`** — For structural tasks (callers, blast-radius, dead-code, semantic-search). Compares response JSON array against ground-truth set. Computes precision, recall, and F1. Normalizes `file:line symbol` format for comparison.

**`qualitative`** (`keyword_presence`) — For conceptual tasks (orient, conventions, refactor). Checks which ground-truth keywords appear in the response text. Score = fraction found.

## Ground truth

Ground-truth files have three tiers:

- **`verified`** — Generated from actual tool queries on pinned repo commits. High confidence.
- **`initial`** — Derived from validation data or manual inspection. Medium confidence.
- **`stub`** — Empty placeholder. Scoring is skipped.
