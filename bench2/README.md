# Scenario-Based Evaluation (bench2)

Human-mind, multi-step benchmark that scores code intelligence tools by how well they help a developer through realistic tasks — exploration, analysis, and planning.

## Overview

6 end-to-end scenarios (one per repo) where Claude works through 4 steps in a single session. Each scenario has a machine-verifiable checklist. Two-layer scoring separates answer quality (fairness) from tool adoption.

## Two-layer scoring

**Fairness score** = 0.70 × correctness + 0.30 × efficiency
- For Sense vs Baseline comparisons
- Excludes `layer: adoption` checks (mcp_tool_used, no_grep)
- Efficiency calibrated per repo size (Flask 15k → Next.js 40k)

**Adoption score** = 0.60 × tool_fluency + 0.40 × discoverability
- For code-intel vs code-intel comparisons only (Sense vs Roam vs Greptile)
- Not used in fairness comparisons

## The 6 scenarios

| Repo | Scenario | What it tests |
|------|----------|---------------|
| **flask** | WSGI dispatch trace + debug parameter | Call graph traversal, test-file mapping, modification impact |
| **gin** | Middleware chain trace + request ID | Go dispatch tracing, middleware flow, dead code detection |
| **axum** | Handler trait propagation + request ID layer | Rust trait analysis, Tower middleware, layered architecture |
| **discourse** | Topic creation flow + authorization | Rails service-object tracing, Guardian auth, spec locating |
| **javalin** | Servlet dispatch + error handler | Java framework tracing, routing table, handler registration |
| **nextjs** | SSR render path + request ID threading | TypeScript monorepo navigation, multi-layer rendering pipeline |

Each scenario has 4 steps: Explore → Analyze → Understand → Plan.

## Directory layout

```
bench2/
├── scenarios/                # One YAML per repo
├── lib/
│   ├── scenario.py           # Parse/validate scenario YAML
│   ├── scorer.py             # Two-layer scoring, check evaluation
│   └── reporter.py           # Comparison tables + aggregate
├── run.sh                    # Runner: tool × scenario → transcript.json
├── score.sh                  # Batch score all transcripts
├── report.sh                 # Produce comparison report
├── improvement-loop/         # Autonomous scenario improvement
│   ├── improve-loop.sh       # Single loop, N iterations, Claude reviewer
│   └── instructions/         # LOOP-CONTEXT.md + phase instructions
├── analysis/                 # Transcript analysis reports
└── results/                  # Scored output
```

## Usage

### 1. Run scenarios

```bash
source bench/.venv/bin/activate

bash bench2/run.sh --dry-run              # See what would execute
bash bench2/run.sh --tool sense --repo flask  # Single scenario
bash bench2/run.sh                            # All scenarios, all tools
```

### 2. Score transcripts

```bash
bash bench2/score.sh                      # Score all
bash bench2/score.sh --tool sense --repo flask  # Score specific
```

### 3. Generate report

```bash
bash bench2/report.sh                     # Terminal table
bash bench2/report.sh --md                # Markdown (writes results/report.md)
bash bench2/report.sh --json              # JSON
```

### 4. Improvement loop (optional)

The improvement loop refines scenario checks so the fairness score gap accurately reflects the qualitative difference visible in transcripts. Each iteration: re-run scenarios → score → Claude reads all transcripts side-by-side → generates `improvements.json` → applies changes → validates (rolls back on regression).

**Prerequisites:** existing transcripts from steps 1-2 above, plus `claude` CLI installed and authenticated (the loop invokes `claude -p` to review transcripts).

**Cost/time:** each iteration runs all scenarios (~$3-6 in API calls, ~15-20 min) plus one `claude -p` reviewer call (~$0.50-1). Default is 3 iterations. The loop stops early if the fairness gap converges (stable within 0.02 between iterations).

```bash
# Dry run — see what would execute without running anything
bash bench2/improvement-loop/improve-loop.sh --dry-run

# Default: 3 iterations, Opus 4.7 as reviewer
bash bench2/improvement-loop/improve-loop.sh

# More iterations, specific repos only
bash bench2/improvement-loop/improve-loop.sh --iterations 5 --repo gin,flask

# Use a cheaper model as reviewer
bash bench2/improvement-loop/improve-loop.sh --reviewer-model claude-sonnet-4-6
```

**All flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--iterations N` | 3 | Max iterations before stopping |
| `--reviewer-model M` | `claude-opus-4-7` | Claude model for transcript review |
| `--model M` | (system default) | Claude model for running scenarios |
| `--repo REPOS` | all 6 | Comma-separated repo filter |
| `--tool TOOLS` | `sense,baseline` | Comma-separated tool filter |
| `--runs N` | 1 | Runs per scenario (for variance) |
| `--dry-run` | | Show plan without executing |

**Output:** results land in `improvement-loop/results/loop-1-iter-N/` per iteration:
- `analysis-notes.md` — per-repo qualitative assessment with gap analysis
- `improvements.json` — evidence-cited check modifications
- `claude-review.log` — full reviewer output
- `post-analysis.json` — scores after applying changes (used for convergence check)
- `regression.json` — if a regression was detected (loop stops with exit code 2)

#### Running the reviewer manually

If you want to run just the transcript review step (skip re-running scenarios), you can invoke the reviewer directly. Copy this prompt and pass it to Claude:

```bash
claude -p "$(cat <<'PROMPT'
Read the following instruction files, then analyze the bench2 transcripts and generate improvements.json.

## Instructions

$(cat bench2/improvement-loop/instructions/LOOP-CONTEXT.md)

---

$(cat bench2/improvement-loop/instructions/phase1-analysis-instruct.md)

---

$(cat bench2/improvement-loop/instructions/phase2-improve-instruct.md)

## Current Scores

$(source bench/.venv/bin/activate && python3 bench2/lib/reporter.py bench2/results --format terminal 2>/dev/null)

## Task

1. Read the sense and baseline transcripts for all 6 repos in bench2/results/
2. Write analysis-notes.md to bench2/improvement-loop/results/analysis-notes.md
3. Write improvements.json to bench2/improvement-loop/results/improvements.json
PROMPT
)" --model claude-opus-4-7 --allowedTools "Read,Bash,Write"
```

The reviewer reads all 12 transcripts, compares answer quality per repo, and generates targeted check modifications with transcript evidence.

## Scenario format

```yaml
name: "Human-readable name"
repo: flask
description: |
  What this scenario tests.

steps:
  - name: "Step title"
    prompt: |
      The prompt for this step.
    checks:
      - type: word
        value: "expected_symbol"
        required: true
        description: "Why this matters"
      - type: mcp_tool_used
        value: sense_graph
        required: false
        layer: adoption          # Excluded from fairness score
        description: "Used graph for caller analysis"

scoring:
  weights:
    correctness: 0.70
    efficiency: 0.30
```

## Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- Python 3.9+ with PyYAML (`pip install pyyaml`)
- Repos bootstrapped under `../sense-benchmark/_reference/`
