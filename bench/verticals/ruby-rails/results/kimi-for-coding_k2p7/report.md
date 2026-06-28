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
| baseline | 65% (11/17) | 65% (11/17) | 151,759 | 131,327 | 2,396,928 | — |
| sense | 100% (17/17) | 100% (17/17) | 109,603 | 89,357 | 2,023,168 | — |

_Billed-context Δ (sense vs baseline): **-28%** — Sense loads less._

### discourse

> Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 100% (24/24) | 50% (12/24) | 235,793 | 201,261 | 5,358,336 | — |
| sense | 96% (23/24) | 83% (20/24) | 132,320 | 111,777 | 1,896,448 | — |

_Billed-context Δ (sense vs baseline): **-44%** — Sense loads less._

### forem

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 81% (21/26) | 65% (17/26) | 143,908 | 123,165 | 1,548,288 | — |
| sense | 81% (21/26) | 69% (18/26) | 91,331 | 75,011 | 1,502,169 | — |

_Billed-context Δ (sense vs baseline): **-37%** — Sense loads less._

### gitlabhq

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 48% (11/23) | 48% (11/23) | 149,258 | 123,855 | 2,050,304 | — |
| sense | 96% (22/23) | 91% (21/23) | 155,527 | 124,832 | 4,965,376 | — |

_Billed-context Δ (sense vs baseline): **+4%** — Sense loads more._

### langchainrb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 97% (28/29) | 76% (22/29) | 88,732 | 78,969 | 949,760 | — |
| sense | 97% (28/29) | 90% (26/29) | 125,490 | 111,359 | 1,775,360 | — |

_Billed-context Δ (sense vs baseline): **+41%** — Sense loads more._

### llm.rb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 70% (19/27) | 52% (14/27) | 118,975 | 107,219 | 1,036,800 | — |
| sense | 89% (24/27) | 85% (23/27) | 93,614 | 82,797 | 1,012,992 | — |

_Billed-context Δ (sense vs baseline): **-21%** — Sense loads less._

### lobsters

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 82% (14/17) | 76% (13/17) | 154,728 | 142,086 | 3,205,376 | — |
| sense | 94% (16/17) | 94% (16/17) | 137,367 | 121,676 | 2,938,880 | — |

_Billed-context Δ (sense vs baseline): **-11%** — Sense loads less._

### mastodon

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 96% (22/23) | 83% (19/23) | 114,489 | 91,682 | 721,408 | — |
| sense | 100% (23/23) | 91% (21/23) | 116,402 | 96,357 | 2,417,664 | — |

_Billed-context Δ (sense vs baseline): **+2%** — Sense loads more._

### rails

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 100% (18/18) | 89% (16/18) | 113,781 | 99,327 | 1,335,078 | — |
| sense | 100% (18/18) | 89% (16/18) | 158,698 | 131,858 | 8,312,832 | — |

_Billed-context Δ (sense vs baseline): **+39%** — Sense loads more._

### raix

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 87% (13/15) | 73% (11/15) | 62,051 | 51,866 | 287,791 | — |
| sense | 87% (13/15) | 87% (13/15) | 72,645 | 64,024 | 500,224 | — |

_Billed-context Δ (sense vs baseline): **+17%** — Sense loads more._

### redmine

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 94% (17/18) | 83% (15/18) | 208,563 | 178,044 | 5,018,112 | — |
| sense | 94% (17/18) | 83% (15/18) | 126,819 | 109,050 | 1,766,912 | — |

_Billed-context Δ (sense vs baseline): **-39%** — Sense loads less._

### ruby_llm

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 87% (20/23) | 78% (18/23) | 131,839 | 115,702 | 2,004,480 | — |
| sense | 87% (20/23) | 74% (17/23) | 126,223 | 107,114 | 3,281,408 | — |

_Billed-context Δ (sense vs baseline): **-4%** — Sense loads less._

### solidus

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 95% (19/20) | 90% (18/20) | 113,041 | 94,341 | 1,200,384 | — |
| sense | 90% (18/20) | 85% (17/20) | 154,100 | 122,147 | 5,715,200 | — |

_Billed-context Δ (sense vs baseline): **+36%** — Sense loads more._

### Aggregate

Ranked by **cited recall** (the headline). **B-score** = `0.55·cited + 0.25·correct-relationship rate + 0.20·truthfulness`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial token usage.

| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | sense :1st_place_medal: | 26 | 0 | 0.8554 | **0.8896** | 0.9315 | 0.8765 | 1.0000 | 0 | 0.0000 | 125,416 | 0.0s | — | 99.9% (3234/3239) |
| 2 | baseline :2nd_place_medal: | 26 | 0 | 0.7157 | **0.7924** | 0.8691 | 0.8038 | 0.9892 | **5** | 0.0000 | 139,539 | 0.0s | — | 99.8% (3086/3092) **!1** |


### Process efficiency (at held recall)

_Sense recall is HIGHER (0.86 vs 0.72) — any process saving is a bonus on top of a completeness win._

| Process axis | baseline | sense | Δ |
|------|---------:|------:|----:|
| Reads | 63 | 46 | **-28%** |
| Tool calls | 93 | 84 | **-10%** |
| Billed tokens | 139,539 | 125,416 | **-10%** |