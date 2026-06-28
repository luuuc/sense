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
| baseline | 35% (6/17) | 29% (5/17) | 647,546 | 638,102 | — | — |
| sense | 76% (13/17) | 71% (12/17) | 840,411 | 831,715 | — | — |

_Billed-context Δ (sense vs baseline): **+30%** — Sense loads more._

### discourse

> Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 62% (15/24) | 46% (11/24) | 1,983,543 | 1,968,574 | — | — |
| sense | 71% (17/24) | 58% (14/24) | 777,050 | 769,793 | — | — |

_Billed-context Δ (sense vs baseline): **-61%** — Sense loads less._

### forem

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 81% (21/26) | 77% (20/26) | 721,161 | 707,241 | — | — |
| sense | 62% (16/26) | 58% (15/26) | 883,492 | 868,368 | — | — |

_Billed-context Δ (sense vs baseline): **+23%** — Sense loads more._

### gitlabhq

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 35% (8/23) | 22% (5/23) | 1,611,785 | 1,597,901 | — | — |
| sense | 74% (17/23) | 74% (17/23) | 568,803 | 551,676 | — | — |

_Billed-context Δ (sense vs baseline): **-65%** — Sense loads less._

### langchainrb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 24% (7/29) | 10% (3/29) | 711,704 | 702,702 | — | — |
| sense | 55% (16/29) | 52% (15/29) | 1,107,142 | 1,100,566 | — | — |

_Billed-context Δ (sense vs baseline): **+56%** — Sense loads more._

### llm.rb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 89% (24/27) | 89% (24/27) | 860,307 | 852,464 | — | — |
| sense | 89% (24/27) | 81% (22/27) | 2,030,978 | 2,020,417 | — | — |

_Billed-context Δ (sense vs baseline): **+136%** — Sense loads more._

### lobsters

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 71% (12/17) | 35% (6/17) | 1,437,252 | 1,426,890 | — | — |
| sense | 76% (13/17) | 59% (10/17) | 1,606,199 | 1,594,720 | — | — |

_Billed-context Δ (sense vs baseline): **+12%** — Sense loads more._

### mastodon

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 52% (12/23) | 48% (11/23) | 1,028,421 | 1,018,105 | — | — |
| sense | 74% (17/23) | 70% (16/23) | 1,171,806 | 1,160,702 | — | — |

_Billed-context Δ (sense vs baseline): **+14%** — Sense loads more._

### rails

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 67% (12/18) | 50% (9/18) | 522,811 | 512,350 | — | — |
| sense | 78% (14/18) | 78% (14/18) | 2,368,478 | 2,343,977 | — | — |

_Billed-context Δ (sense vs baseline): **+353%** — Sense loads more._

### raix

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 87% (13/15) | 87% (13/15) | 276,276 | 272,868 | — | — |
| sense | 87% (13/15) | 80% (12/15) | 1,231,535 | 1,223,200 | — | — |

_Billed-context Δ (sense vs baseline): **+346%** — Sense loads more._

### redmine

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 78% (14/18) | 56% (10/18) | 2,042,413 | 2,028,695 | — | — |
| sense | 83% (15/18) | 72% (13/18) | 1,560,230 | 1,546,617 | — | — |

_Billed-context Δ (sense vs baseline): **-24%** — Sense loads less._

### ruby_llm

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 61% (14/23) | 52% (12/23) | 759,358 | 752,729 | — | — |
| sense | 48% (11/23) | 39% (9/23) | 1,424,632 | 1,415,120 | — | — |

_Billed-context Δ (sense vs baseline): **+88%** — Sense loads more._

### solidus

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 35% (7/20) | 25% (5/20) | 1,043,605 | 1,027,858 | — | — |
| sense | 65% (13/20) | 60% (12/20) | 1,805,653 | 1,793,692 | — | — |

_Billed-context Δ (sense vs baseline): **+73%** — Sense loads more._

### Aggregate

Ranked by **cited recall** (the headline). **B-score** = `0.55·cited + 0.25·correct-relationship rate + 0.20·truthfulness`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial token usage.

| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | sense :1st_place_medal: | 26 | 0 | 0.6191 | **0.7128** | 0.7379 | 0.6924 | 0.9962 | **2** | 0.0000 | 1,215,844 | 0.0s | — | 88.9% (2505/2817) **!17** |
| 2 | baseline :2nd_place_medal: | 26 | 0 | 0.4429 | **0.5871** | 0.6278 | 0.5785 | 0.9945 | **2** | 0.0000 | 1,067,682 | 0.0s | — | 95.7% (2679/2799) **!17** |


### Process efficiency (at held recall)

_Sense recall is HIGHER (0.62 vs 0.44) — any process saving is a bonus on top of a completeness win._

| Process axis | baseline | sense | Δ |
|------|---------:|------:|----:|
| Reads | 31 | 37 | **+22%** |
| Tool calls | 56 | 63 | **+13%** |
| Billed tokens | 1,067,682 | 1,215,844 | **+14%** |