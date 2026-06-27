# Vertical benches

## Goal

A vertical bench exists to make Sense better and to prove it brings high value to
any project built on a given tech stack. By benchmarking Sense against a no-Sense
baseline across many real, popular open-source repos in one language/framework
(Rails, Django, and so on), each vertical measures where Sense already helps an
AI coding agent and surfaces the gaps worth fixing. Every project on that stack
inherits the gains.

## What a vertical is

A vertical (for example `ruby-rails` or `python-django`) is a baseline-vs-Sense
benchmark run against real, popular open-source repos from that stack's
ecosystem, not toy projects. The `ruby-rails` vertical, for instance, benches
Discourse, Mastodon, GitLab, Chatwoot, Forem, Solidus, Redmine, Rails itself,
and more (see `<name>/repos.txt`). These are large, widely used codebases, so a
win means Sense helps an agent on the kind of project people actually build.

Each vertical compares exactly two arms, **baseline** (no Sense) and **sense**,
across several AI coding tools and models. The runs spend real tokens (API or
subscription), so the numbers reflect what a developer actually pays for.

This differs from the global competitor bench in `../global/`, which pits Sense
against Serena, GitNexus, and a probe inside Docker. A vertical is just the two
arms on real tools.

Each vertical is fully self-contained in its own directory. The engine is generic
and selects a vertical with `VERTICAL=<name>`, so nothing here is special-cased.

```
verticals/
└── <name>/                     e.g. ruby-rails
    ├── repos.txt               membership list (human-facing; not read by code)
    ├── scenarios/              one <repo>.yaml + <repo>.rubric.yaml per repo
    └── results/                all scored/judged output, model-scoped
        ├── <model>/            e.g. claude-opus-4-8, gpt-5.5, kimi-for-coding_k2p7
        │   └── <arm>/<repo>/   arm is baseline or sense
        │       ├── transcript.json   what the agent did
        │       ├── scored.json       objective metrics (recall, efficiency, ...)
        │       ├── judged.json       LLM-judge per-step scores
        │       ├── run_meta.json     exit code, token/answer stats
        │       └── run-1/ run-2/ ... one subdir per run when benched N times
        ├── variance/<repo>.md  per-repo N-run spread + means
        ├── report.md           cross-model matrix (tracked, the durable board)
        └── report.json         same, machine-readable
```

Model ids are sanitized for the dir name, with `/` and `:` becoming `_`
(`ollama-cloud/qwen3-coder-next` becomes `ollama-cloud_qwen3-coder-next`).

## Tools and models

A vertical is swept across these arms. Each runs both baseline and sense; the
model id selects the harness and the results directory.

| Tool (harness) | Model | Model id (results dir) |
|---|---|---|
| Claude Code | Opus 4.8 | `claude-opus-4-8` |
| Codex | GPT-5.5 | `gpt-5.5` |
| Opencode | Kimi K2.7 | `kimi-for-coding/k2p7` (`kimi-for-coding_k2p7`) |
| Opencode | Qwen 3 Coder Next | `ollama-cloud/qwen3-coder-next` (`ollama-cloud_qwen3-coder-next`) |
| Opencode | Devstral Small 2 | `ollama-cloud/devstral-small-2:24b` (`ollama-cloud_devstral-small-2_24b`) |

Claude Code on Opus 4.8 is the headline arm; the others show the value holds
across tools and model tiers. The judge stays `claude-sonnet-4-6`.

## Where to look for results and scores

| You want | Look at |
|---|---|
| The headline board (Sense delta per repo, all models) | `<name>/results/report.md` |
| One model's board, regenerated from disk | `python3 ../lib/scoreboard.py <name>/results/<model>` |
| Variance (per-run spread + mean) for a repo | `<name>/results/variance/<repo>.md` |
| Objective metrics for one cell | `<name>/results/<model>/<arm>/<repo>/scored.json` |
| What the agent actually did | `.../<arm>/<repo>/transcript.json` (or `run-N/transcript.json`) |
| Per-repo discriminator-group breakdown | `python3 ../lib/pergroup.py <repo>` (reads `RESULTS_DIR`) |

`report.md` and `report.json` are regenerated. The per-model copies are
gitignored; the cross-model matrix at the results root is tracked as the durable
evidence.

## How a vertical bench runs

The roots are resolved by `../lib/bench-paths.sh` from `VERTICAL` (and, for
sweeps, `BENCH_MODEL`). You normally never set these by hand; the drivers do.
Everything routes through the shared engine at the bench root (`../score.sh`,
`../judge.sh`, `../report.sh`) plus the per-model runner chosen by model id.

The drivers live in `../drivers/`. The two you will use most:

### Variance sweep, the headline mechanism (recommended)

Runs each model N times, both arms, and writes the per-run spread plus means. A
single run lies, so ship variance. `RUNS=2` is the settled standard.

```bash
source ../.venv/bin/activate
MODELS="claude-opus-4-8" RUNS=2 bash ../drivers/runs-variance.sh <repo>
# writes verticals/<name>/results/variance/<repo>.md (plus scored/judged per run)
```

### Single-snapshot sweep, to fill a model x repo matrix

One run per (model, repo), idempotent (skips cells already benched), refreshes
the cross-model matrix at the end.

```bash
MODELS="claude-opus-4-8" REPOS="chatwoot discourse" bash ../drivers/sweep.sh
```

Both default to `VERTICAL=ruby-rails` and `MODELS=claude-opus-4-8`. For another
vertical, set it explicitly:

```bash
VERTICAL=python-django MODELS="claude-opus-4-8" REPOS="<repos>" \
  bash ../drivers/sweep.sh
```

The runner is dispatched by model id. `claude-*` goes to `bench-sense-local.sh`,
`gpt-*` and `codex:*` to `codex-run.sh`, and `kimi-for-coding/*`, `*:cloud`,
`ollama-cloud/*` (and the other opencode coding-plan providers) to
`opencode-run.sh`.

### Re-score, re-judge, or re-report without re-running

`score.sh` and `judge.sh` are idempotent and cheap. To re-score against changed
gold for $0 (no new session), point the engine at the existing transcripts.

```bash
VERTICAL=<name> BENCH_MODEL=<model> bash ../score.sh        # re-score
VERTICAL=<name> BENCH_MODEL=<model> bash ../judge.sh        # re-judge
VERTICAL=<name> bash ../drivers/report-matrix.sh            # refresh report.md/json
```

## Prerequisites

Repos are cloned and indexed under `$SENSE_BENCH_ROOT` (default
`../../../sense-benchmark`, a sibling of the sense repo) at their pinned commits
(`../PINNED_COMMITS.json`). Before benching a vertical's repos for the first time,
or after a scan-engine change, reindex them.

```bash
bash ../drivers/rescan-all.sh        # (re)index this vertical's repos, smallest to biggest
```

## Adding a new vertical

Two drivers automate the mechanics; the judgment (repo choice, scenario authoring)
stays manual by design.

1. **Stamp the dirs:** `bash ../drivers/new-vertical.sh <name>` creates
   `verticals/<name>/scenarios/` + `repos.txt` (and a local doc skeleton). Then fill
   `repos.txt` and pick the 6 repos.
2. **Per repo, run the loop:** `bash ../drivers/vertical-loop.sh <repo>` (set
   `VERTICAL=<name>`). It chains index → scout → preflight → bench → report → harvest
   and STOPS at the human gates (scenario authoring, tie diagnosis); pass `--yes` to
   clear the cost gate before the paid Opus×2 sweep. The `results/` tree is created on
   first run and the matrix appears at `results/report.md`.

Done by hand, that loop is: `mkdir -p verticals/<name>/scenarios` + a `repos.txt`;
author `<repo>.yaml` + `<repo>.rubric.yaml`; clone/index under `$SENSE_BENCH_ROOT`
(`rescan-all.sh`); then `VERTICAL=<name> ... bash ../drivers/runs-variance.sh <repo>`.
No code changes are required either way, because `bench-paths.sh` derives every path
from `<name>`.
