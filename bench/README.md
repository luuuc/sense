# Scenario-Based Evaluation (bench)

Multi-step benchmark that scores code-intelligence tools by how well
they help an **AI agent** work through realistic exploration, analysis,
and modification tasks. Six end-to-end scenarios per tool, each judged
on answer quality (LLM judge), citation grounding, keyword coverage,
and efficiency.

## Overview

6 scenarios (one per repo), 4 steps each, run in a single Claude
session per `(tool, scenario)` pair. Two-layer scoring:

- **Fairness** — for sense vs baseline. The headline number.
- **Adoption** — for code-intel vs code-intel comparisons only
  (sense vs roam vs greptile). Reported alongside but never folded
  into fairness.

Full formula and component definitions: see [`SCORING.md`](./SCORING.md).
End-goal and bench-readiness criteria: see [`end-goal.md`](./end-goal.md).

## Fairness formula (summary)

```
fairness = 0.10·keyword_coverage + 0.55·llm_quality
         + 0.15·citation_grounding + 0.20·efficiency
```

- `llm_quality` (55%) is the headline — Claude Opus 4.7 judges each
  step against a per-scenario rubric.
- `citation_grounding` (15%) verifies every `file:line` reference in
  the answer against the repo at `run_meta.repo_commit`.
- `efficiency` (20%) is half tokens, half wall-time, calibrated
  per-repo.
- `keyword_coverage` (10%) is a smoke test, not the headline.

The four axes are locked in [`locked/locked.yaml`](./locked/locked.yaml).

## The 6 scenarios

| Repo | Scenario | What it tests |
|------|----------|---------------|
| **flask** | WSGI dispatch trace + debug parameter | Call graph traversal, test-file mapping, modification impact |
| **gin** | Middleware chain trace + request ID | Go dispatch tracing, middleware flow, dead code detection |
| **axum** | Handler trait propagation + request ID layer | Rust trait analysis, Tower middleware, layered architecture |
| **discourse** | Topic creation flow + authorization | Rails service-object tracing, Guardian auth, spec locating |
| **javalin** | Servlet dispatch + error handler | Java framework tracing, routing table, handler registration |
| **nextjs** | SSR render path + request ID threading | TypeScript monorepo navigation, multi-layer rendering pipeline |

Three additional **held-out** scenarios live under
`scenarios/held-out/` — `flask-blueprints`, `axum-towers`,
`sense-mcp-flow` — with hand-graded `gold.json` reference scores.
The improvement loop never edits these; they anchor convergence
against human judgment.

## Directory layout

```
bench/
├── scenarios/                # One YAML + rubric YAML per repo
│   └── held-out/             # Frozen scenarios + gold grades + lockfile-pinned transcripts
├── verticals/                # Per-vertical benches, each fully self-contained
│   └── ruby-rails/           #   repos.txt + scenarios/ + results/<model>/
├── lib/                      # Shared engine (vertical-agnostic); bench-paths.sh resolves roots
│   ├── scorer.py             # Per-step check evaluation, efficiency, ceilings
│   ├── fairness.py           # Combined fairness formula (the source of truth)
│   ├── grounding.py          # file:line citation extraction + verification
│   ├── judge.py              # LLM-as-judge per step
│   ├── judge_prompt.v1.md    # Locked judge prompt
│   ├── reporter.py           # Comparison tables + aggregate
│   ├── audit_scoring.py      # Score-auditor (per-step quality re-check)
│   ├── audit_scenarios.py    # Scenario auditor (proposes check additions/edits)
│   ├── audit_watchdog.py     # Anti-Goodhart watchdog (flags suspect iterations)
│   ├── convergence.py        # 4-criteria stop evaluator
│   ├── delta_report.py       # Per-iteration delta.md
│   ├── readiness.py          # bench-readiness.md verdict
│   ├── cost_tracker.py       # Public-API-priced cost accounting
│   ├── heldout_rescore.py    # Re-judges frozen held-out transcripts each iter
│   ├── lock_check.py         # Validates improvements.json against locked.yaml
│   ├── meta_report.py        # Per-iteration meta narrative
│   ├── variance.py           # Judge-variance baseline tool
│   ├── scenario.py           # Parse/validate scenario YAML
│   └── load-env.sh           # .env → ANTHROPIC_API_KEY mapping
├── score.sh                  # Batch score all transcripts (shared engine)
├── judge.sh                  # Run LLM judge over all scored.json (shared engine)
├── report.sh                 # Comparison report — terminal / md / json (shared engine)
├── drivers/                  # Vertical-campaign drivers (run ANY vertical)
│   ├── sweep.sh              #   model sweep: each repo × model, both arms
│   ├── sweep-breadth.sh      #   breadth-first metered sweep (+ sweep-resume.sh)
│   ├── runs-variance.sh      #   ×N variance runner
│   ├── session-run.sh        #   multi-turn ("work session") bench
│   ├── report-matrix.sh      #   cross-model matrix → verticals/<name>/results/report.md
│   ├── rescan-all.sh         #   (re)index a vertical's repos
│   ├── codex-run.sh          #   GPT-5.x via Codex CLI (+ opencode-run.sh for cloud models)
│   ├── bench-sense-local.sh  #   local sense-vs-baseline runner (gitignored)
│   └── check-articles.sh     #   article freshness + structure gate
├── global/                   # Original cross-competitor DOCKER bench (dormant)
│   ├── bench.sh              #   one-shot: run → score → judge → report
│   ├── run.sh                #   per-tool docker runner
│   ├── build.sh              #   build bench-* images (+ build-prescan.sh)
│   ├── freeze-heldout.sh     #   one-time held-out transcript freezer
│   ├── docker/               #   per-tool images (sense, serena, gitnexus, probe, baseline)
│   └── locked/               #   locked.yaml + held-out.lock (held-out fairness pins)
└── results/                  # GLOBAL bench scored output + report.md
```

## Usage

### TL;DR — run a full bench

```bash
source bench/.venv/bin/activate
bash bench/global/bench.sh    # run + score + judge + report (md + json)
```

Total ~$8-19 and ~20 min for a fresh full run. With no flags: all
6 scenarios × both tools = 12 sessions. Forwards `--tool` / `--repo`
filters to the run/score/judge stages.

Under the hood, `global/bench.sh` chains four idempotent scripts you
can also invoke individually (`score.sh`/`judge.sh`/`report.sh` are the
shared engine at the bench root; `run.sh` is the global docker runner):

| Script | Cost | Time | What |
|---|---|---|---|
| `global/run.sh` | ~$5-13 | ~15 min | Runs Claude sessions, writes `transcript.json` |
| `score.sh` | free | seconds | Computes `keyword_coverage`, `citation_grounding`, `efficiency`, `adoption_score` → `scored.json` |
| `judge.sh` | ~$3-6 | ~3 min | Opus 4.7 judges each step against the rubric → `judged.json` |
| `report.sh --md` / `--json` | free | seconds | Combines into `results/report.{md,json}` |

`score.sh` and `judge.sh` are idempotent — re-running is cheap. To
re-score against changed scenarios without burning a session, skip
`run.sh`.

### 1. Run scenarios

```bash
source bench/.venv/bin/activate

bash bench/global/run.sh --dry-run                  # See what would execute
bash bench/global/run.sh --tool sense --repo flask  # Single scenario
bash bench/global/run.sh                             # All scenarios, all tools
```

Per-session budgets and timeouts are derived per repo from
`BUDGET_PER_REPO` and `TIME_CEILINGS` in `lib/scorer.py`. Override
globally with `--budget USD` or `--timeout SECS`.

### 2. Score transcripts

```bash
bash bench/score.sh                          # Score all
bash bench/score.sh --tool sense --repo flask
```

Writes `scored.json` next to each `transcript.json`. Idempotent —
re-running is cheap.

### 3. Run the LLM judge

```bash
bash bench/judge.sh                          # Judge all
bash bench/judge.sh --tool sense --repo flask
bash bench/judge.sh --force                  # Re-judge even if up-to-date
```

Writes `judged.json` next to each `scored.json`. Skips when
`judged.json` is newer than its `transcript.json` unless `--force`.
Requires `BENCHMARK_ANTHROPIC_API_KEY` in `.env` (see `lib/load-env.sh`).
Without judge output, the report renders fairness as `—`.

### 4. Generate report

```bash
bash bench/report.sh                         # Terminal table
bash bench/report.sh --md                    # Markdown → results/report.md
bash bench/report.sh --json                  # JSON → results/report.json
```

## Scenario format

```yaml
name: Flask WSGI dispatch trace and debug instrumentation
repo: flask
description: |
  Trace the Flask WSGI request dispatch pipeline end-to-end, locate
  the test coverage for the core dispatch, then assess what would
  break if you added a debug parameter to wsgi_app.

steps:
  - name: Trace the request dispatch pipeline
    prompt: |
      You're about to insert a request-scoped hook into Flask's
      dispatch pipeline. Hand the next agent the map: starting from
      Flask.wsgi_app, list every method called in order through to
      finalize_request, each with file:line, one-line behaviour,
      and what it calls next.
    checks:
      - type: word
        value: wsgi_app
        required: true
        description: Core dispatch entry point
      - type: contains
        value: app.py:1625
        required: false
        description: Cited the precise line where __call__ invokes wsgi_app
      - type: mcp_tool_used
        value: sense_graph
        required: false
        layer: adoption          # Excluded from fairness
        description: Used graph for caller analysis
```

Check types: `contains`, `phrase`, `word`, `starts_with`, `exact`,
`mcp_tool_used`, `no_grep`, `diff_contains`, `response_richness`.
Full descriptions in [`SCORING.md`](./SCORING.md).

Each scenario also has a `<repo>.rubric.yaml` driving the LLM judge
(four weighted criteria per step — typically `map_quality`,
`specificity`, `justification`, `uncertainty`).

## Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- Python 3.9+ with PyYAML (`pip install pyyaml`)
- Repos bootstrapped under `../sense-benchmark/_reference/`
- `.env` at repo root with `BENCHMARK_ANTHROPIC_API_KEY=…`. `lib/load-env.sh` maps it to `ANTHROPIC_API_KEY` for child processes. Judge + audit are direct-API; `claude` CLI scenario sessions fall back to OAuth subscription on credit exhaustion (one retry with the key unset).
