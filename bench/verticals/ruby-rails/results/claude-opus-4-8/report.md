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
| baseline | 47% (8/17) | 24% (4/17) | 23,599 | 2,402 | 673,655 | 257s |
| sense | 100% (17/17) | 100% (17/17) | 17,770 | 2,690 | 368,986 | 183s |

_Billed-context Δ (sense vs baseline): **-25%** — Sense loads less._

### discourse

> Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 79% (19/24) | 29% (7/24) | 25,565 | 2,653 | 945,882 | 287s |
| sense | 96% (23/24) | 88% (21/24) | 34,548 | 3,076 | 845,356 | 356s |

_Billed-context Δ (sense vs baseline): **+35%** — Sense loads more._

### forem

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 73% (19/26) | 15% (4/26) | 28,705 | 2,930 | 676,358 | 308s |
| sense | 58% (15/26) | 50% (13/26) | 21,307 | 2,694 | 552,060 | 231s |

_Billed-context Δ (sense vs baseline): **-26%** — Sense loads less._

### gitlabhq

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 26% (6/23) | 17% (4/23) | 28,036 | 2,697 | 884,530 | 317s |
| sense | 78% (18/23) | 74% (17/23) | 33,419 | 7,159 | 1,096,958 | 384s |

_Billed-context Δ (sense vs baseline): **+19%** — Sense loads more._

### langchainrb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 52% (15/29) | 21% (6/29) | 14,550 | 2,271 | 455,835 | 168s |
| sense | 72% (21/29) | 31% (9/29) | 20,892 | 4,230 | 496,646 | 190s |

_Billed-context Δ (sense vs baseline): **+44%** — Sense loads more._

### llm.rb

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 93% (25/27) | 48% (13/27) | 21,050 | 2,402 | 873,564 | 250s |
| sense | 93% (25/27) | 52% (14/27) | 15,676 | 405 | 536,794 | 201s |

_Billed-context Δ (sense vs baseline): **-26%** — Sense loads less._

### lobsters

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 88% (15/17) | 71% (12/17) | 21,559 | 2,412 | 986,557 | 282s |
| sense | 88% (15/17) | 82% (14/17) | 21,712 | 2,825 | 664,637 | 240s |

_Billed-context Δ (sense vs baseline): **+1%** — Sense loads more._

### mastodon

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 65% (15/23) | 30% (7/23) | 30,703 | 2,539 | 986,971 | 341s |
| sense | 91% (21/23) | 83% (19/23) | 35,851 | 13,549 | 447,335 | 268s |

_Billed-context Δ (sense vs baseline): **+17%** — Sense loads more._

### rails

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 78% (14/18) | 72% (13/18) | 25,365 | 3,274 | 607,127 | 291s |
| sense | 100% (18/18) | 89% (16/18) | 19,354 | 2,702 | 554,952 | 220s |

_Billed-context Δ (sense vs baseline): **-24%** — Sense loads less._

### raix

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 87% (13/15) | 53% (8/15) | 10,535 | 2,257 | 212,578 | 100s |
| sense | 87% (13/15) | 67% (10/15) | 13,438 | 2,557 | 239,520 | 134s |

_Billed-context Δ (sense vs baseline): **+28%** — Sense loads more._

### redmine

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 83% (15/18) | 67% (12/18) | 25,499 | 2,410 | 737,167 | 283s |
| sense | 94% (17/18) | 78% (14/18) | 22,892 | 2,829 | 660,193 | 251s |

_Billed-context Δ (sense vs baseline): **-10%** — Sense loads less._

### ruby_llm

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 87% (20/23) | 78% (18/23) | 19,218 | 2,410 | 990,804 | 230s |
| sense | 83% (19/23) | 78% (18/23) | 19,150 | 2,835 | 827,247 | 211s |

_Billed-context Δ (sense vs baseline): **-0%** — Sense loads less._

### solidus

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 85% (17/20) | 40% (8/20) | 26,479 | 2,412 | 934,928 | 307s |
| sense | 80% (16/20) | 65% (13/20) | 27,232 | 2,837 | 942,431 | 286s |

_Billed-context Δ (sense vs baseline): **+3%** — Sense loads more._

### Aggregate

Ranked by **cited recall** (the headline). **B-score** = `0.55·cited + 0.25·correct-relationship rate + 0.20·truthfulness`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial token usage.

| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | sense :1st_place_medal: | 26 | 0 | 0.7271 | **0.8052** | 0.9105 | 0.8212 | 1.0000 | 0 | 0.3669 | 23,203 | 245.2s | $36.74 | 98.0% (3060/3121) |
| 2 | baseline :2nd_place_medal: | 26 | 0 | 0.4640 | **0.6354** | 0.8055 | 0.7280 | 0.9911 | **4** | 0.3357 | 23,198 | 267.2s | $38.32 | 98.2% (2594/2642) |


### Process efficiency (at held recall)

_Sense recall is HIGHER (0.73 vs 0.46) — any process saving is a bonus on top of a completeness win._

| Process axis | baseline | sense | Δ |
|------|---------:|------:|----:|
| Reads | 12 | 14 | **+11%** |
| Tool calls | 27 | 25 | **-7%** |
| Billed tokens | 23,198 | 23,203 | **+0%** |