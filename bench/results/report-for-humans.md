# bench — Code-Intelligence Tools for AI Agents

A side-by-side comparison of five ways an AI coding agent can navigate
an unfamiliar codebase. Each tool runs the same six end-to-end
exploration scenarios (one per repo) and is scored on answer quality,
citation grounding, keyword coverage, and efficiency.

If you only read one number from this report: **Sense wins all six
scenarios on fairness, often by a healthy margin, while costing the
least and being the fastest.**

> Companion docs:
> [README](../README.md) ·
> [Scoring methodology](../SCORING.md) ·
> [End-goal & readiness criteria](../end-goal.md) ·
> [Auto-generated leaderboard (`report.md`)](report.md) ·
> [Citation hallucinations](citation-hallucinations.md)

## The contenders

| Tool | Version | Source | Backing technology |
|---|---|---|---|
| **sense** | `0.84.1` (schema v4, embeddings `all-MiniLM-L6-v2-ctx1`) | [luuuc/sense](https://github.com/luuuc/sense) | Pre-built symbol graph + embeddings, served over MCP |
| **gitnexus** | `1.6.3` | [abhigyanpatwari/GitNexus](https://github.com/abhigyanpatwari/GitNexus) | Code knowledge graph with Cypher-style queries |
| **probe** | `probe-code 0.6.0` | [probelabs/probe](https://github.com/probelabs/probe) | Stateless tree-sitter + ripgrep at query time |
| **serena** | `1.3.0` | [oraios/serena](https://github.com/oraios/serena) | LSP-driven symbol cache (per-language servers) |
| **baseline** | n/a (Claude Code only) | — | grep / find / Read — no MCP server |

All five run inside the same Claude Code harness (Opus 4.7, 1M
context) against the same six repos pinned at the same commits. Only
the MCP configuration changes between runs.

## The six benchmark repos

| Repo | Language / framework | Pinned commit | Scenario YAML |
|---|---|---|---|
| [tokio-rs/axum](https://github.com/tokio-rs/axum) | Rust · Tower-based async web framework | `9f4d52e3` | [`axum.yaml`](../scenarios/axum.yaml) |
| [discourse/discourse](https://github.com/discourse/discourse) | Ruby on Rails · forum platform | `d73e4484b4b` | [`discourse.yaml`](../scenarios/discourse.yaml) |
| [pallets/flask](https://github.com/pallets/flask) | Python · WSGI micro-framework | `2ac89889` | [`flask.yaml`](../scenarios/flask.yaml) |
| [gin-gonic/gin](https://github.com/gin-gonic/gin) | Go · HTTP web framework | `d3ffc998` | [`gin.yaml`](../scenarios/gin.yaml) |
| [javalin/javalin](https://github.com/javalin/javalin) | Java / Kotlin · lightweight web framework | `7078afcc7` | [`javalin.yaml`](../scenarios/javalin.yaml) |
| [vercel/next.js](https://github.com/vercel/next.js) | TypeScript · React SSR monorepo | `16e5f9e6851` | [`nextjs.yaml`](../scenarios/nextjs.yaml) |

## A note on the score scale

Scores like `0.820` and `0.45` are fractions of `1.0`. They could be
read as percentages — `82.0%` and `45%` — and that is how this
human-facing report renders them. The machine-generated
[`report.md`](report.md) keeps the raw `0–1` form so downstream
tooling parses unambiguous numbers.

## How a single tool earns its rank

```
fairness = 0.10·keyword_coverage  (smoke test)
         + 0.55·llm_quality       (Opus-4.7 judge, per-step rubric)
         + 0.15·citation_grounding (file:line verified vs. checkout)
         + 0.20·efficiency         (½ tokens, ½ wall time, calibrated per repo)
```

`llm_quality` is the headline (55%). The other axes catch the failure
modes a single judge score misses: a beautiful but unverifiable answer
gets penalized on grounding; a slow drift through grep gets penalized
on efficiency. Full definitions live in [`SCORING.md`](../SCORING.md).

`Adoption` (separate column) measures how often the agent reached for
the MCP server vs. falling back to grep. It matters when comparing
two code-intel tools to each other, not when comparing one to the
no-MCP baseline.

## Reading the scores

| Column | Best | Meaning |
|---|---|---|
| Fairness | higher | Weighted composite (formula above). The headline rank. |
| Adoption | higher | How fluently the agent used the MCP tools vs falling back to grep. Only meaningful for tool-vs-tool comparisons; baseline has no MCP. |
| Keyword | higher | Hit rate on a per-scenario keyword smoke-test. 10% of fairness — a sanity check, not the headline. |
| Quality | higher | Opus-4.7 judge score against the per-repo rubric. 55% of fairness — the headline component. |
| Eff. | higher | Half token efficiency, half time efficiency, each calibrated per repo. 20% of fairness. |
| Tokens | lower | Billed input + output tokens (uncached) for the full scenario. |
| Time | lower | Wall-clock from prompt-sent to final-result. |
| Cost | lower | Anthropic API spend in USD for the scenario. |
| Cites | higher | `grounded / total` file-line citations verified against the repo checkout at `run_meta.repo_commit`. Folded into fairness at 15%. |
| Savings | higher | Cost reduction vs the baseline run on the same scenario, in %. `—` on the baseline row itself. Positive = cheaper than baseline. |

## Aggregate leaderboard

| Rank | Tool | Avg Fairness | Avg LLM Quality | Avg Grounding | Avg Tokens | Avg Time | Total Cost | Savings |
|---:|---|---:|---:|---:|---:|---:|---:|---:|
| **#1** | **sense** :1st_place_medal: | **81.3%** | 85.4% | **89.2%** | **10,896** | **141.5s** | **$6.22** | **+17.8%** |
| **#2** | probe :2nd_place_medal: | 77.7% | 84.8% | 72.8% | 12,119 | 162.7s | $6.23 | +17.7% |
| **#3** | baseline :3rd_place_medal: | 77.2% | 84.2% | 80.8% | 12,716 | 185.4s | $7.57 | — |
| **#4** | serena | 75.2% | 83.4% | 61.9% | 14,800 | 191.4s | $7.57 | 0.0% |
| **#5** | gitnexus | 74.9% | 84.5% | 76.9% | 12,964 | 173.8s | $6.87 | +9.2% |

Sense leads the pack across every aggregate metric: best fairness,
best judge quality, highest citation grounding, fewest tokens,
fastest, and cheapest. Probe is the strongest non-sense tool overall;
gitnexus and serena both fall slightly *behind* the baseline (Claude
with grep) on fairness — meaning their MCP servers, as configured, add
friction without offsetting it with quality.

## Per-scenario results

Each scenario is a four-step exploration script: trace some dispatch,
locate the relevant tests, and assess a non-trivial modification (e.g.
"thread a request-ID through this layer"). It is judged by Opus-4.7
against a per-repo rubric and verified against the actual repo
checkout for citation grounding.

### axum — Rust trait propagation + request-ID layer

[`scenarios/axum.yaml`](../scenarios/axum.yaml) ·
[`axum.rubric.yaml`](../scenarios/axum.rubric.yaml) ·
repo [tokio-rs/axum](https://github.com/tokio-rs/axum) @ `9f4d52e3`

> Trace `Handler` trait propagation, understand extractor chaining, and
> add a request-ID layer. Tests Rust trait analysis, Tower middleware
> comprehension, and layered modification.

| Rank | Tool | Fairness | Keyword | Quality | Eff. | Tokens | Time | Cost | Cites | Savings |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| #1 | sense | 82.0% | 100% | 88% | 45% | 14,141 | 188s | $1.42 | 65/67 | -0.3% |
| #2 | baseline | 81.7% | 100% | 86% | 52% | 11,857 | 181s | $1.41 | 60/65 | — |
| #3 | gitnexus | 79.1% | 97% | 88% | 48% | 13,768 | 170s | $1.18 | 58/75 | +16.6% |
| #4 | serena | 78.6% | 97% | 86% | 37% | 16,051 | 223s | $1.45 | 22/23 | -2.4% |
| #5 | probe | 77.7% | 100% | 87% | 44% | 13,535 | 217s | $1.50 | 11/15 | -5.7% |

### discourse — Rails topic-creation flow + authorization

[`scenarios/discourse.yaml`](../scenarios/discourse.yaml) ·
[`discourse.rubric.yaml`](../scenarios/discourse.rubric.yaml) ·
repo [discourse/discourse](https://github.com/discourse/discourse) @ `d73e4484b4b`

> Trace topic creation from controller to persistence, locate the
> specs, understand `Guardian` authorization. Tests Rails service-object
> tracing and test convention awareness.

| Rank | Tool | Fairness | Keyword | Quality | Eff. | Tokens | Time | Cost | Cites | Savings |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| #1 | sense | 84.2% | 97% | 86% | 60% | 13,197 | 176s | $1.41 | 36/36 | -6.4% |
| #2 | probe | 83.6% | 82% | 90% | 55% | 14,925 | 192s | $1.06 | 56/57 | +20.1% |
| #3 | gitnexus | 80.8% | 82% | 89% | 67% | 10,673 | 149s | $1.03 | 38/55 | +22.1% |
| #4 | serena | 80.2% | 97% | 84% | 51% | 16,374 | 208s | $1.17 | 45/48 | +11.9% |
| #5 | baseline | 79.6% | 85% | 86% | 49% | 16,325 | 229s | $1.33 | 50/54 | — |

### flask — WSGI dispatch + debug parameter

[`scenarios/flask.yaml`](../scenarios/flask.yaml) ·
[`flask.rubric.yaml`](../scenarios/flask.rubric.yaml) ·
repo [pallets/flask](https://github.com/pallets/flask) @ `2ac89889`

> Trace WSGI dispatch, locate the tests, add a debug parameter, verify
> the change. Tests call-graph traversal, test-file mapping, and safe
> code-modification awareness.

| Rank | Tool | Fairness | Keyword | Quality | Eff. | Tokens | Time | Cost | Cites | Savings |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| #1 | sense | 84.8% | 100% | 85% | 68% | 6,255 | 72s | $0.47 | 50/51 | -4.0% |
| #2 | probe | 82.2% | 95% | 83% | 60% | 7,521 | 94s | $0.62 | 11/11 | -38.3% |
| #3 | gitnexus | 81.9% | 97% | 86% | 48% | 9,188 | 134s | $0.72 | 14/14 | -61.8% |
| #4 | serena | 79.0% | 78% | 81% | 59% | 7,645 | 98s | $0.51 | 11/11 | -14.1% |
| #5 | baseline | 77.0% | 95% | 86% | 62% | 7,121 | 89s | $0.45 | 9/17 | — |

### gin — Go middleware chain + recovery edit

[`scenarios/gin.yaml`](../scenarios/gin.yaml) ·
[`gin.rubric.yaml`](../scenarios/gin.rubric.yaml) ·
repo [gin-gonic/gin](https://github.com/gin-gonic/gin) @ `d3ffc998`

> Understand middleware chaining, trace HTTP dispatch, find dead code,
> modify the recovery middleware. Tests data-flow tracing, dead-code
> detection, and structural editing awareness.

| Rank | Tool | Fairness | Keyword | Quality | Eff. | Tokens | Time | Cost | Cites | Savings |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| #1 | sense | 80.7% | 89% | 80% | 64% | 7,148 | 81s | $0.66 | 52/52 | +28.8% |
| #2 | baseline | 74.0% | 92% | 78% | 35% | 11,925 | 164s | $0.93 | 75/75 | — |
| #3 | serena | 73.2% | 86% | 82% | 22% | 16,832 | 182s | $1.54 | 52/52 | -66.1% |
| #4 | probe | 72.5% | 89% | 76% | 33% | 12,439 | 162s | $0.94 | 58/58 | -1.6% |
| #5 | gitnexus | 68.9% | 94% | 75% | 15% | 17,103 | 224s | $1.38 | 71/71 | -49.2% |

### javalin — Java servlet dispatch + error handler

[`scenarios/javalin.yaml`](../scenarios/javalin.yaml) ·
[`javalin.rubric.yaml`](../scenarios/javalin.rubric.yaml) ·
repo [javalin/javalin](https://github.com/javalin/javalin) @ `7078afcc7`

> Understand servlet dispatch, trace routing table construction, add a
> custom error handler. Tests Java framework comprehension and
> handler-registration patterns.

| Rank | Tool | Fairness | Keyword | Quality | Eff. | Tokens | Time | Cost | Cites | Savings |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| #1 | sense | 75.4% | 100% | 89% | 38% | 13,306 | 171s | $1.08 | 23/38 | +14.3% |
| #2 | probe | 73.9% | 100% | 86% | 50% | 10,691 | 140s | $0.86 | 28/65 | +32.0% |
| #3 | baseline | 70.3% | 100% | 89% | 33% | 13,801 | 198s | $1.26 | 19/57 | — |
| #4 | gitnexus | 65.3% | 100% | 86% | 39% | 12,674 | 177s | $1.15 | 0/6 | +8.8% |
| #5 | serena | 62.1% | 100% | 82% | 25% | 18,576 | 244s | $1.48 | 11/88 | -17.5% |

### nextjs — TypeScript SSR render path + request-ID threading

[`scenarios/nextjs.yaml`](../scenarios/nextjs.yaml) ·
[`nextjs.rubric.yaml`](../scenarios/nextjs.rubric.yaml) ·
repo [vercel/next.js](https://github.com/vercel/next.js) @ `16e5f9e6851`

> Trace the SSR render path, understand route matching, thread a
> request ID. Tests TypeScript monorepo navigation and complex
> server-side pipeline understanding.

| Rank | Tool | Fairness | Keyword | Quality | Eff. | Tokens | Time | Cost | Cites | Savings |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| #1 | sense | 80.9% | 88% | 85% | 75% | 11,326 | 161s | $1.18 | 31/44 | +45.9% |
| #2 | baseline | 80.3% | 91% | 80% | 63% | 15,270 | 252s | $2.19 | 23/24 | — |
| #3 | serena | 78.1% | 97% | 85% | 70% | 13,322 | 194s | $1.42 | 18/35 | +35.1% |
| #4 | probe | 76.1% | 91% | 86% | 71% | 13,602 | 172s | $1.26 | 15/40 | +42.6% |
| #5 | gitnexus | 73.6% | 91% | 83% | 69% | 14,379 | 189s | $1.40 | 9/26 | +36.2% |

## The tools, in plain English

### #1 — Sense ([luuuc/sense](https://github.com/luuuc/sense))

**What it does.** Sense indexes a repository once (symbols,
references, embeddings) and exposes the result over four MCP calls:
`sense_search` (semantic), `sense_graph` (callers/callees),
`sense_blast` (impact analysis), `sense_conventions` (project
patterns).

**Why it wins.** It is the only tool that combines *structural*
answers ("who calls X?", "what breaks if I change Y?") with *semantic*
search ("find code that does authorization") in one place. The agent
rarely has to fall back to grep, so it spends fewer tokens reading raw
files, and the answers cite tighter `file:line` references that ground
at ~89%.

**Pros**
- 1st place on all 6 scenarios on fairness
- Cheapest total cost and fastest avg wall-time across the six runs
- Highest citation grounding (89.2%) — fewest hallucinated `file:line`
- Adoption score 0.75 — agents pick it up readily over grep
- Lightweight footprint: ~80 MB CPU-only embeddings
  (`all-MiniLM-L6-v2-ctx1`), no GPU required, no per-query model download
- Self-refreshing index: incremental `sense scan` only re-processes
  changed files; `sense scan --watch` keeps the index live via fsnotify;
  every MCP response surfaces a stale-files count so the agent (and you)
  notice drift — no manual maintenance after the first scan

**Cons**
- One-time `sense scan` is needed before the first query (~minutes on
  most repos; ~20 min on a 75k-symbol monorepo like Next.js).
  Subsequent scans are incremental and fast
- On-disk index lives under `.sense/` per repo — fine on a workstation,
  something to size if running inside small CI containers

**Pick sense when:** you want the agent to navigate an unfamiliar
codebase fast, with verifiable citations, at the lowest cost.

### #2 — Probe ([probelabs/probe](https://github.com/probelabs/probe))

**What it does.** No on-disk index. Every query parses the touched
files on the fly with tree-sitter and runs ripgrep underneath, with
the MCP layer adding scoping (search inside a file, around a symbol).

**Why it places.** Stateless means zero setup; the agent gets useful
structural search without ever waiting for an index build. Quality
holds up — across the six scenarios, probe matches sense on judge
score within 0.6 percentage points — but it tends to over-fetch (more
tokens, slower).

**Pros**
- Zero index, zero state — works on any repo immediately
- Strong judge quality (avg 84.8% — within 0.6 pts of sense)
- 100% grounding on three of six scenarios (when it cites, it's right)

**Cons**
- Roughly 11% more tokens and 15% more wall-time than sense
- Adoption is the lowest of all MCP tools (0.30) — agents tend to keep
  using grep alongside it
- Citation *count* is lower on harder scenarios (11/15 on axum, 15/40
  on nextjs) — it answers in prose more than `file:line`

**Pick probe when:** you can't afford an indexing step (e.g.,
one-shot analysis on a fresh checkout, or a repo that's being
rewritten).

### #3 — Baseline (Claude Code with grep / find / Read)

**What it does.** No MCP at all. The agent uses the same primitives a
human dev would: `grep -r`, `find`, `Read`. This is the floor every
real tool has to beat.

**Why it places.** Claude is good at grep. On simple repos (flask,
axum) it gets within a few percent of the top score on judge quality.
What it lacks is *efficiency*: it reads broadly, costs more in tokens,
and takes longer to converge.

**Pros**
- Zero setup, runs anywhere Claude Code runs
- Judge quality is competitive (avg 84.2% — only 1.2 pts below sense)
- A useful sanity check: any MCP tool that loses to baseline isn't
  earning its keep

**Cons**
- 21% more wall-time and ~$1.35 more per 6-scenario run than sense
- Adoption layer is irrelevant — there's no MCP to fluently use
- Citations are sometimes invented when the agent reasons from a file
  it didn't fully open (see
  [`citation-hallucinations.md`](citation-hallucinations.md))

**Pick baseline when:** you want a control, or when the repo is too
small to justify any MCP overhead.

### #4 — Serena ([oraios/serena](https://github.com/oraios/serena))

**What it does.** Spins up real language servers (Pyright, gopls,
rust-analyzer, etc.), serializes their symbol graphs into a local
`.serena/cache/`, and exposes `find_symbol` / `find_references` over
MCP.

**Why it places.** Its judge quality is solid (avg 83.4%), but it
pays a heavy efficiency tax: serena needed the most tokens (avg
14,800) and the most wall-time (avg 191s) of any tool. Its citation
grounding is the lowest of any MCP tool (61.9%) — the LSP cache
often points at the right symbol but the wrong line.

**Pros**
- Highest adoption when agents use it well (0.79 on discourse)
- Symbol awareness is real LSP-grade (precise type-aware navigation
  per-language)
- Works well on Ruby (discourse) and Java (when grounding aside)

**Cons**
- Slowest tool overall (avg 191s) and most tokens (avg 14,800)
- Lowest citation grounding (61.9%) — many cited lines drift
- LSP startup cost is non-trivial; needs a healthy language server
  per language (Java/Kotlin/Rust each need their own)

**Pick serena when:** you need *language-aware* precision (refactors,
rename-symbol, find-references) more than you need answer-level
search.

### #5 — Gitnexus ([abhigyanpatwari/GitNexus](https://github.com/abhigyanpatwari/GitNexus))

**What it does.** Builds a graph database (`.gitnexus/`) from the
repo. The MCP exposes Cypher-like queries plus a registry so the agent
can list and switch between indexed repos.

**Why it places.** When gitnexus is configured correctly, its answers
ground well (e.g., 38/55 on discourse, 71/71 on gin). But it sits at
the bottom of the aggregate because it spends *more* tokens than the
baseline on three scenarios (gin, nextjs, javalin), and on javalin it
emitted just 6 citations — none of which grounded.

**Pros**
- Cross-repo registry — useful if you bounce between projects
- Strong on Go and Ruby (gin: 71/71 cites; discourse: 38/55 cites)
- Slightly higher adoption (0.47) than serena's symbol approach

**Cons**
- Last on aggregate fairness (74.9%)
- Wall-time and tokens both higher than sense and baseline
- javalin grounding collapsed (0/6) — the graph schema may not capture
  Java handler registration patterns

**Pick gitnexus when:** you specifically need Cypher-style queries
across multiple repos, and your stack is Go or Ruby.

## How to read the cost / time numbers

Every cell measures the *full* run: model API calls, MCP tool calls,
prompt overhead, the lot. Costs are billed Anthropic tokens, no
caching credits subtracted. Wall-time is the harness-measured duration
from prompt-sent to final-result-emitted.

A scenario with a $1.50 cost means "the agent burned $1.50 to answer
all four steps of this scenario." Multiply by your own scenario count
to project a real workload.

## Reproducing this report

```bash
# Index/check all five tools across the six repos
bash bench/run.sh

# Score keyword + grounding + efficiency
bash bench/score.sh

# LLM-judge each step (Opus 4.7 against per-repo rubric)
bash bench/judge.sh

# Render the report
bash bench/report.sh --md      # → results/report.md
bash bench/report.sh --json    # → results/report.json
```

Each `(tool, repo)` cell produces:

```
bench/results/<tool>/<repo>/
├── transcript.json   # full Claude Code stream-json
├── scored.json       # keyword + grounding + efficiency
├── judged.json       # per-step LLM judge scores
└── run_meta.json     # commit pinned, wall-time, model used
```

The headline [`report.md`](report.md) / [`report.json`](report.json)
are derived from these per-cell files; nothing in the report can drift
from the raw data.

---

*— Luc B. Perussault-Diallo, 2026-05-14*
