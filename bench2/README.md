# Scenario-Based Evaluation (bench2)

Human-mind, multi-step benchmark that scores code intelligence tools by how well they help a developer through realistic tasks — exploration, analysis, and planning.

## Overview

Instead of 10 isolated single-turn Q&A scored against grep-generated ground truth, this harness runs **6 end-to-end scenarios** (one per repo) where Claude works through multiple steps in a single session. Each scenario has a machine-verifiable checklist. The score captures completeness, efficiency, fluency (MCP tool usage vs. grep fallback), and cost.

## Key differences from bench/

| Aspect | bench/ (competitive) | bench2/ (scenario) |
|--------|---------------------|---------------------|
| Session shape | 10 single-prompt tasks | 6 multi-step scenarios (one per repo) |
| Ground truth | grep-generated, 11/28 broken | Human-curated checklists, machine-verifiable |
| Scoring | F1 against broken GT | Checklist hit rate + miss detection + efficiency |
| Sessions needed | ~358-490 | ~42 (7 tools × 6 repos) or ~84 with 2 runs |
| Per-session cost | ~$0.05 | ~$0.10-0.20 (longer, multi-step) |
| Face validity | Low (0.0 F1 on blast-radius) | High (real developer tasks) |
| Explainable? | No (cryptic normalization) | Yes (step checklist = what was found/missed) |
| Measures misses | Per-session | Per-step (richer signal) |
| Measures time | Session total | Session total (could add per-step) |

## The 6 scenarios

| Repo | Scenario | What it tests |
|------|----------|---------------|
| **flask** | WSGI dispatch trace + debug parameter | Call graph traversal, test-file mapping, modification impact |
| **gin** | Middleware chain trace + request ID | Go dispatch tracing, middleware flow, dead code detection |
| **axum** | Handler trait propagation + request ID layer | Rust trait analysis, Tower middleware, layered architecture |
| **discourse** | Topic creation flow + authorization | Rails service-object tracing, Guardian auth, spec locating |
| **javalin** | Servlet dispatch + error handler | Java framework tracing, routing table, handler registration |
| **nextjs** | SSR render path + request ID threading | TypeScript monorepo navigation, multi-layer rendering pipeline |

Each scenario has 4 steps:

1. **Explore** — trace a code path, find key symbols, understand how things connect
2. **Analyze** — find callers, map dependencies, locate tests
3. **Understand** — grasp a pattern (middleware, auth, error handling)
4. **Plan** — assess impact of a realistic change

## Directory layout

```
bench2/
├── scenarios/                # One YAML per repo — the evaluation scripts
│   ├── flask.yaml
│   ├── gin.yaml
│   ├── axum.yaml
│   ├── discourse.yaml
│   ├── javalin.yaml
│   └── nextjs.yaml
├── tools/                    # Symlinks to bench/tools/*.sh
├── lib/
│   ├── scenario.py           # Parse/validate scenario YAML, build full prompt
│   ├── scorer.py             # Check list matching, miss detection, per-step metrics
│   └── reporter.py           # Per-scenario comparison tables + aggregate
├── run.sh                    # Runner: tool × scenario → transcript.json
├── score.sh                  # Batch score all transcripts
├── report.sh                 # Produce comparison report
├── README.md                 # This file
└── results/                  # Scored output (gitignored)
```

## Scoring per scenario

Each scenario has ~15-25 checklist items across its 4 steps. Items are one of:

| Check type | Verification |
|-----------|-------------|
| `contains` | `value` appears case-insensitively in the transcript or tool outputs |
| `exact` | `value` appears verbatim in the transcript |
| `transcript_contains` | Same as `contains` but scoped to assistant text only |
| `diff_contains` | `value` appears in `git diff` output at session end |

Items are marked `required: true` (counts fully) or `required: false` (bonus/partial credit).

**Score components:** completeness (checklist hit rate), accuracy (same score), efficiency (tokens normalized), tool fluency (grep fallback penalty), discoverability (did it use MCP tools or bypass them).

**Miss detection:** same logic as bench/ — did Claude reach for grep/Read/Glob when MCP tools were available? But now per-step in a multi-step conversation, giving richer signal.

## Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- Python 3.9+ with PyYAML (`pip install pyyaml`)
- Repos bootstrapped under `../sense-benchmark/_reference/` (use `bench/bootstrap-repos.sh`)
- Tool binaries installed (or rely on symlinked tool scripts)

```bash
python3 -m venv bench/.venv
source bench/.venv/bin/activate
pip install -r bench/requirements.txt
```

bench2/ shares the same virtualenv and repo layout as bench/ — no separate venv or repo bootstrap needed.

## Usage

### 1. Run scenarios

```bash
source bench/.venv/bin/activate

# Dry-run: see what would execute and estimated cost
bash bench2/run.sh --dry-run

# Run a single scenario with a single tool
bash bench2/run.sh --tool sense --repo flask

# Run all scenarios with all tools
bash bench2/run.sh

# With 2 runs each (for variance estimation)
bash bench2/run.sh --runs 2

# Run multiple tools against the same repo sequentially (see note below)
bash bench2/run.sh --tool sense --repo flask && bash bench2/run.sh --tool baseline --repo flask
```

**Important:** Do NOT run the same repo with different tools in parallel. Each tool modifies shared `~/.claude/.mcp.json` during setup/cleanup. Run sequentially (chain with `&&`) or use different repos. Running different repos in parallel is safe.
```

### 2. Score transcripts

```bash
# Score all
bash bench2/score.sh

# Score specific
bash bench2/score.sh --tool sense --repo flask
```

### 3. Generate report

```bash
# Terminal table
bash bench2/report.sh

# Markdown (writes results/report.md)
bash bench2/report.sh --md

# JSON (writes results/report.json)
bash bench2/report.sh --json
```

## Scenario format

```yaml
name: "Human-readable name"
repo: flask            # Must match a directory in _reference/
description: |
  Multi-line description of what this scenario tests.

steps:
  - name: "Step title"
    prompt: |
      The prompt for this step. Claude receives all steps as one
      continuous prompt and works through them sequentially.
    checks:
      - type: contains           # Check type
        value: "expected text"   # What to match in the transcript
        required: true           # true = full credit, false = bonus
        description: "Why"       # Optional, appears in report

scoring:
  weights:
    completeness: 0.35
    accuracy: 0.25
    efficiency: 0.20
    tool_fluency: 0.10
    discoverability: 0.10
```

## Output format

### `scored.json` (per tool/repo)

```json
{
  "scenario": "Flask WSGI dispatch trace...",
  "repo": "flask",
  "overall_score": 0.782,
  "completeness": 0.85,
  "efficiency": 0.92,
  "fluency": 0.90,
  "steps": [
    {
      "name": "Orient and understand dispatch",
      "combined_score": 0.88,
      "hits_required": 7,
      "total_required": 8,
      "checks": [...]
    }
  ],
  "misses": {
    "total": 1,
    "calls": ["grep(pattern=TODO)"],
    "detail": "MCP was used, but also fell back to grep/Read"
  },
  "metrics": {
    "token_total": 12400,
    "tool_calls": 8,
    "wall_time_seconds": 42.3,
    "cost_usd": 0.062
  }
}
```

### `report.md` (aggregate)

```
## Scenario Evaluation

### flask
| Rank | Tool | Score | Completeness | Fluency | Tokens | Misses | Time | Cost |
|-----:|------|------:|-------------:|--------:|-------:|-------:|-----:|-----:|
| 1 | sense | 0.782 | 85.0% | 90.0% | 12,400 | 1 | 42.3s | $0.06 |
| 2 | roam | 0.654 | ... | ... | ... | ... | ... | ... |

### Aggregate
| Rank | Tool | Scenarios | Avg Score | Avg Completeness | Avg Tokens | Avg Misses | Total Cost |
|-----:|------|----------:|----------:|-----------------:|-----------:|-----------:|-----------:|
| 1 | sense | 6 | 0.7200 | 0.7800 | 14,200 | 0.8 | $0.42 |
```

## Extending

- **Add a scenario:** Create `scenarios/<repo>.yaml`, ensure the repo exists in `_reference/`, run.
- **Add a tool:** Add `tools/<tool>.sh` implementing the setup/ready/write-config protocol (see bench/tools/protocol.md for the contract). Symlink to bench/tools/ if reusing.
- **Add a check type:** Modify `evaluate_check()` in `lib/scorer.py` and validate the new type string.
