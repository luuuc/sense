## Scenario Evaluation

Results: 2 tools × 16 scenarios

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
| baseline | 47% (8/17) | 47% (8/17) | 179,192 | 169,440 | 686,464 | — |
| sense | 82% (14/17) | 82% (14/17) | 185,101 | 173,085 | 878,720 | — |

_Billed-context Δ (sense vs baseline): **+3%** — Sense loads more._

### discourse

> Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 58% (14/24) | 58% (14/24) | 205,011 | 190,260 | 1,122,432 | — |
| sense | 79% (19/24) | 79% (19/24) | 163,807 | 147,826 | 2,235,264 | — |

_Billed-context Δ (sense vs baseline): **-20%** — Sense loads less._

### forem

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 58% (15/26) | 58% (15/26) | 165,992 | 154,915 | 478,720 | — |
| sense | 69% (18/26) | 62% (16/26) | 140,371 | 129,026 | 1,020,288 | — |

_Billed-context Δ (sense vs baseline): **-15%** — Sense loads less._

### gitlabhq

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 39% (9/23) | 39% (9/23) | 141,144 | 130,810 | 508,160 | — |
| sense | 91% (21/23) | 87% (20/23) | 138,859 | 124,412 | 1,188,352 | — |

_Billed-context Δ (sense vs baseline): **-2%** — Sense loads less._

### langchainrb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 100% (29/29) | 100% (29/29) | 125,567 | 117,442 | 286,336 | — |
| sense | 93% (27/29) | 93% (27/29) | 154,392 | 140,860 | 861,568 | — |

_Billed-context Δ (sense vs baseline): **+23%** — Sense loads more._

### llm.rb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 96% (26/27) | 93% (25/27) | 134,400 | 126,683 | 556,032 | — |
| sense | 100% (27/27) | 100% (27/27) | 138,225 | 123,321 | 1,339,648 | — |

_Billed-context Δ (sense vs baseline): **+3%** — Sense loads more._

### llm.rb.before-steering

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| sense | 100% (27/27) | 100% (27/27) | 170,293 | 154,357 | 2,029,312 | — |

### lobsters

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 82% (14/17) | 82% (14/17) | 178,561 | 165,345 | 930,688 | — |
| sense | 94% (16/17) | 94% (16/17) | 140,828 | 128,333 | 1,525,376 | — |

_Billed-context Δ (sense vs baseline): **-21%** — Sense loads less._

### mastodon

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 57% (13/23) | 57% (13/23) | 146,620 | 137,052 | 353,920 | — |
| sense | 91% (21/23) | 91% (21/23) | 122,691 | 112,042 | 469,120 | — |

_Billed-context Δ (sense vs baseline): **-16%** — Sense loads less._

### rails

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 83% (15/18) | 83% (15/18) | 110,528 | 97,402 | 675,584 | — |
| sense | 100% (18/18) | 100% (18/18) | 129,990 | 119,377 | 493,312 | — |

_Billed-context Δ (sense vs baseline): **+18%** — Sense loads more._

### raix

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 87% (13/15) | 87% (13/15) | 76,194 | 67,570 | 355,584 | — |
| sense | 87% (13/15) | 87% (13/15) | 79,491 | 70,241 | 499,584 | — |

_Billed-context Δ (sense vs baseline): **+4%** — Sense loads more._

### redmine

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 78% (14/18) | 67% (12/18) | 177,653 | 167,827 | 784,640 | — |
| sense | 83% (15/18) | 83% (15/18) | 159,133 | 150,491 | 307,712 | — |

_Billed-context Δ (sense vs baseline): **-10%** — Sense loads less._

### redmine.before-steering

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| sense | 94% (17/18) | 83% (15/18) | 198,661 | 186,606 | 1,121,024 | — |

### ruby_llm

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 83% (19/23) | 83% (19/23) | 106,707 | 97,529 | 677,504 | — |
| sense | 83% (19/23) | 83% (19/23) | 114,179 | 104,302 | 798,592 | — |

_Billed-context Δ (sense vs baseline): **+7%** — Sense loads more._

### solidus

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 75% (15/20) | 75% (15/20) | 209,873 | 195,580 | 880,768 | — |
| sense | 100% (20/20) | 100% (20/20) | 130,521 | 118,208 | 777,472 | — |

_Billed-context Δ (sense vs baseline): **-38%** — Sense loads less._

### solidus.before-steering

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| sense | 100% (20/20) | 100% (20/20) | 265,745 | 252,738 | 1,139,968 | — |

### Aggregate

Ranked by **cited recall** (the headline). **B-score** = `0.55·cited + 0.25·correct-relationship rate + 0.20·truthfulness`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial token usage.

| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | sense :1st_place_medal: | 19 | 0 | 0.8581 | **0.8865** | 0.8761 | 0.8617 | 0.9955 | **1** | 0.0000 | 154,010 | 0.0s | — | 98.5% (3850/3908) **!1** |
| 2 | baseline :2nd_place_medal: | 16 | 0 | 0.7272 | **0.7726** | 0.7134 | 0.6936 | 0.9960 | **1** | 0.0000 | 155,571 | 0.0s | — | 98.6% (3322/3370) **!3** |


### Process efficiency (at held recall)

_Sense recall is HIGHER (0.86 vs 0.73) — any process saving is a bonus on top of a completeness win._

| Process axis | baseline | sense | Δ |
|------|---------:|------:|----:|
| Reads | 0.0 | 0.0 | — |
| Tool calls | 30 | 61 | **+102%** |
| Billed tokens | 155,571 | 154,010 | **-1%** |