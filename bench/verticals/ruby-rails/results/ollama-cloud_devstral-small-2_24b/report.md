## Scenario Evaluation

Results: 2 tools × 13 scenarios

Each scenario declares a **must-find set** of code locations a good answer should surface. The headline metric is **cited recall** — the share of that set the answer pinned to an exact location (`path:line`), so an agent can navigate straight there. Each repo below leads with a table of the axes that make up the comparison:

- **Cited recall (the headline)** — share of the must-find set pinned to an exact location (`path:line`, `path (line N)`, a `"line": N` field, or an unambiguous name + line).
- **Mention recall** — share the answer named at all, location optional (how complete the map is).
- **Billed context** — billed tokens (uncached input + output) used to produce the answer, with uncached input shown alongside. Lower is better; never traded against recall.

The aggregate adds the **B-score** = `0.55·cited recall + 0.25·correct-relationship rate + 0.20·truthfulness` — one blended number for the whole answer. Efficiency is reported separately and only credited when recall holds.

**Citations** are `file:line` / `file:Symbol` references the answer printed. Each is checked against the repo at the benchmarked commit; the ones that did not resolve are listed in [`citation-hallucinations.md`](citation-hallucinations.md).

### Reading the scores

| Metric | Best | Meaning |
|--------|------|---------|
| cited_recall | Higher | The headline. Of the must-find items the scenario declares, the share the answer pinned to an exact location (`path:line`) so an agent can jump straight there. |
| b_score | Higher | One blended score: 55% cited recall + 25% correct-relationship rate + 20% truthfulness. A single number for the whole answer's quality. |
| relationship_audit | Higher | Coverage: the share of the must-find set the answer named at all, graded against the authored relationships. |
| related_recall | Higher | Coverage with the CORRECT relationship stated, not just the name. Naming an endpoint is easy; stating how it connects is the harder test. |
| grounded_precision | Higher | Truthfulness: of the items the answer described, the share described correctly (1 minus false-claims over described). |
| contradictions | Lower | Count of confidently false relationship claims. The fabrication signal. |
| process_efficiency | Lower | Reads, tool calls, and billed tokens spent — credited as a saving only when recall is at least as high as the baseline, so a cheaper-but-thinner answer never wins. |
| efficiency | Higher | Combined token and time efficiency, calibrated per repo. |
| tokens | Lower | Billed (uncached) tokens — lower is cheaper. |
| wall_time | Lower | Wall-clock time. |
| cost_usd | Lower | API cost in USD. |
| cites | Higher | Citations that resolved against the repo checkout: `grounded/total`. A trailing **!N** flags line numbers past end-of-file (made-up). Reported, not folded into the headline. |

### chatwoot

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 18% (3/17) | 6% (1/17) | 1,096,967 | 1,092,102 | — | — |
| sense | 82% (14/17) | 82% (14/17) | 614,733 | 609,879 | — | — |

_Billed-context Δ (sense vs baseline): **-44%** — Sense loads less._

### discourse

> Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 33% (8/24) | 33% (8/24) | 399,856 | 391,350 | — | — |
| sense | 38% (9/24) | 29% (7/24) | 137,102 | 135,443 | — | — |

_Billed-context Δ (sense vs baseline): **-66%** — Sense loads less._

### forem

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 19% (5/26) | 4% (1/26) | 203,116 | 201,683 | — | — |
| sense | 38% (10/26) | 35% (9/26) | 366,179 | 361,628 | — | — |

_Billed-context Δ (sense vs baseline): **+80%** — Sense loads more._

### gitlabhq

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 26% (6/23) | 4% (1/23) | 5,259,702 | 5,246,803 | — | — |
| sense | 30% (7/23) | 22% (5/23) | 245,817 | 242,119 | — | — |

_Billed-context Δ (sense vs baseline): **-95%** — Sense loads less._

### langchainrb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 93% (27/29) | 38% (11/29) | 959,708 | 954,812 | — | — |
| sense | 38% (11/29) | 10% (3/29) | 1,383,914 | 1,380,959 | — | — |

_Billed-context Δ (sense vs baseline): **+44%** — Sense loads more._

### llm.rb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 26% (7/27) | 11% (3/27) | 2,190,026 | 2,186,481 | — | — |
| sense | 44% (12/27) | 26% (7/27) | 1,092,096 | 1,088,787 | — | — |

_Billed-context Δ (sense vs baseline): **-50%** — Sense loads less._

### lobsters

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 59% (10/17) | 41% (7/17) | 822,852 | 814,136 | — | — |
| sense | 59% (10/17) | 41% (7/17) | 2,517,269 | 2,514,343 | — | — |

_Billed-context Δ (sense vs baseline): **+206%** — Sense loads more._

### mastodon

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 17% (4/23) | 13% (3/23) | 1,019,924 | 1,010,046 | — | — |
| sense | 48% (11/23) | 43% (10/23) | 1,580,047 | 1,574,420 | — | — |

_Billed-context Δ (sense vs baseline): **+55%** — Sense loads more._

### rails

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 50% (9/18) | 22% (4/18) | 2,316,028 | 2,311,477 | — | — |
| sense | 61% (11/18) | 61% (11/18) | 1,653,951 | 1,648,287 | — | — |

_Billed-context Δ (sense vs baseline): **-29%** — Sense loads less._

### raix

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 73% (11/15) | 73% (11/15) | 458,241 | 454,930 | — | — |
| sense | 60% (9/15) | 47% (7/15) | 1,341,277 | 1,338,402 | — | — |

_Billed-context Δ (sense vs baseline): **+193%** — Sense loads more._

### redmine

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 61% (11/18) | 28% (5/18) | 1,959,159 | 1,952,506 | — | — |
| sense | 83% (15/18) | 78% (14/18) | 4,941,264 | 4,929,014 | — | — |

_Billed-context Δ (sense vs baseline): **+152%** — Sense loads more._

### ruby_llm

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 39% (9/23) | 26% (6/23) | 1,719,366 | 1,713,991 | — | — |
| sense | 39% (9/23) | 39% (9/23) | 2,094,116 | 2,090,784 | — | — |

_Billed-context Δ (sense vs baseline): **+22%** — Sense loads more._

### solidus

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 35% (7/20) | 25% (5/20) | 1,177,630 | 1,175,116 | — | — |
| sense | 35% (7/20) | 35% (7/20) | 2,834,016 | 2,826,845 | — | — |

_Billed-context Δ (sense vs baseline): **+141%** — Sense loads more._

### Aggregate

Ranked by **cited recall** (the headline). **B-score** = `0.55·cited + 0.25·correct-relationship rate + 0.20·truthfulness`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial token usage.

| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | sense :1st_place_medal: | 26 | 0 | 0.4727 | **0.5973** | 0.5680 | 0.5513 | 0.9973 | **1** | 0.0105 | 1,572,784 | 0.0s | — | 98.6% (1253/1271) **!1** |
| 2 | baseline :2nd_place_medal: | 26 | 0 | 0.2467 | **0.4547** | 0.4969 | 0.4785 | 0.9968 | **1** | 0.0000 | 2,044,783 | 0.0s | — | 100.0% (663/663) |


### Process efficiency (at held recall)

_Sense recall is HIGHER (0.47 vs 0.25) — any process saving is a bonus on top of a completeness win._

| Process axis | baseline | sense | Δ |
|------|---------:|------:|----:|
| Reads | 19 | 8 | **-55%** |
| Tool calls | 37 | 34 | **-7%** |
| Billed tokens | 2,044,783 | 1,572,784 | **-23%** |
