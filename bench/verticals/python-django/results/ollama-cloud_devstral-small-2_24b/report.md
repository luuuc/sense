## Scenario Evaluation

Results: 2 tools × 6 scenarios

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

### healthchecks

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 90% (18/20) | 90% (18/20) | 622,177 | 613,832 | — | — |
| sense | 95% (19/20) | 95% (19/20) | 4,930,149 | 4,923,096 | — | — |

_Billed-context Δ (sense vs baseline): **+692%** — Sense loads more._

### litellm

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 30% (7/23) | 30% (7/23) | 350,531 | 341,013 | — | — |
| sense | 35% (8/23) | 35% (8/23) | 267,990 | 262,835 | — | — |

_Billed-context Δ (sense vs baseline): **-24%** — Sense loads less._

### netbox

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 39% (9/23) | 26% (6/23) | 1,780,856 | 1,771,806 | — | — |
| sense | 70% (16/23) | 70% (16/23) | 199,489 | 193,625 | — | — |

_Billed-context Δ (sense vs baseline): **-89%** — Sense loads less._

### saleor

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 54% (7/13) | 54% (7/13) | 561,122 | 556,463 | — | — |
| sense | 77% (10/13) | 77% (10/13) | 1,106,731 | 1,095,705 | — | — |

_Billed-context Δ (sense vs baseline): **+97%** — Sense loads more._

### sentry

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 18% (3/17) | 18% (3/17) | 78,343 | 75,666 | — | — |
| sense | 47% (8/17) | 47% (8/17) | 246,064 | 240,968 | — | — |

_Billed-context Δ (sense vs baseline): **+214%** — Sense loads more._

### wagtail

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 8% (1/13) | 8% (1/13) | 175,698 | 173,269 | — | — |
| sense | 62% (8/13) | 62% (8/13) | 177,401 | 173,630 | — | — |

_Billed-context Δ (sense vs baseline): **+1%** — Sense loads more._

### Aggregate

Ranked by **cited recall** (the headline). **B-score** = `0.55·cited + 0.25·correct-relationship rate + 0.20·truthfulness`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial token usage.

| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | sense :1st_place_medal: | 12 | 0 | 0.6313 | **0.6772** | 0.6117 | 0.5401 | 0.9750 | **3** | 0.0000 | 816,574 | 0.0s | — | 99.4% (794/799) **!4** |
| 2 | baseline :2nd_place_medal: | 12 | 0 | 0.4574 | **0.5670** | 0.5066 | 0.4617 | 1.0000 | 0 | 0.0000 | 680,422 | 0.0s | — | 97.7% (779/797) **!5** |


### Process efficiency (at held recall)

_Sense recall is HIGHER (0.63 vs 0.46) — any process saving is a bonus on top of a completeness win._

| Process axis | baseline | sense | Δ |
|------|---------:|------:|----:|
| Reads | 6 | 4 | **-40%** |
| Tool calls | 20 | 20 | **-4%** |
| Billed tokens | 680,422 | 816,574 | **+20%** |
