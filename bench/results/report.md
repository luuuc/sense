## Scenario Evaluation

Results: 5 tools × 6 scenarios

Two-layer scoring: **Fairness** = 0.10·keyword_coverage + 0.55·llm_quality + 0.15·citation_grounding + 0.20·efficiency. The judge layer (llm_quality) is the 55% headline — keyword overlap dropped to a 10% smoke test. Fairness cells render `—` if `judge.sh` has not been run on a result. **Adoption** (tool fluency + discoverability) is for code-intel-vs-code-intel comparisons only.

**Citations** are `file.ext:line` or `file.ext:Symbol` references the assistant printed in its answer. The scorer checks each one against the repo at `run_meta.repo_commit`. A `0/0` Cites cell means the answer had no structured citations to verify — neither penalized nor rewarded; prose-only claims are scored by `llm_quality` instead. The full list of ungrounded citations lives in [`citation-hallucinations.md`](citation-hallucinations.md).

### Reading the scores

| Metric | Best | Meaning |
|--------|------|---------|
| fairness | Higher | Combined fairness score — 0.10·keyword_coverage + 0.55·llm_quality + 0.15·citation_grounding + 0.20·efficiency. Shown as `—` if judge.sh has not run yet. |
| adoption | Higher | Adoption score — tool fluency + discoverability, for code-intel comparisons only |
| keyword_coverage | Higher | Hit rate across keyword smoke-test checks (sum of hits / sum of totals; bonus weighted 0.5). Now a 10% smoke test, not the headline. |
| llm_quality | Higher | Judge-rated answer quality, per-scenario mean of step_quality. 0.55 weight in fairness — the headline. |
| efficiency | Higher | Half token efficiency + half time efficiency, each calibrated per repo |
| tokens | Lower | Billed tokens (uncached) — lower is better (cheaper) |
| wall_time | Lower | Wall-clock time — lower is better, folded into efficiency |
| cost_usd | Lower | API cost in USD — lower is better |
| cites | Higher | Citations grounded against the repo checkout: `grounded/total`. A trailing **!N** marks line numbers beyond EOF — outright fabrication. Folded into fairness at 15%. |

### axum

> Multi-step Axum refactoring: trace Handler trait propagation, understand extractor chaining, add a request ID layer. Tests Rust trait analysis, Tower middleware comprehension, and layered modification.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.820 | 0.677 | 100% | 0.88 | 0.45 | 14,141 | 188.2s | $1.42 | 65/67 |
| 2 | baseline :2nd_place_medal: | 0.817 | 0.400 | 100% | 0.86 | 0.52 | 11,857 | 180.6s | $1.41 | 60/65 |
| 3 | gitnexus :3rd_place_medal: | 0.791 | 0.455 | 97% | 0.88 | 0.48 | 13,768 | 170.1s | $1.18 | 58/75 |
| 4 | serena | 0.786 | 0.367 | 97% | 0.86 | 0.37 | 16,051 | 222.6s | $1.45 | 22/23 |
| 5 | probe | 0.777 | 0.240 | 100% | 0.87 | 0.44 | 13,535 | 217.3s | $1.50 | 11/15 |

### discourse

> Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.842 | 0.589 | 97% | 0.86 | 0.60 | 13,197 | 175.8s | $1.41 | 36/36 |
| 2 | probe :2nd_place_medal: | 0.836 | 0.400 | 82% | 0.90 | 0.55 | 14,925 | 191.6s | $1.06 | 56/57 |
| 3 | gitnexus :3rd_place_medal: | 0.808 | 0.538 | 82% | 0.89 | 0.67 | 10,673 | 148.5s | $1.03 | 38/55 |
| 4 | serena | 0.802 | 0.791 | 97% | 0.84 | 0.51 | 16,374 | 207.6s | $1.17 | 45/48 |
| 5 | baseline | 0.796 | 0.400 | 85% | 0.86 | 0.49 | 16,325 | 228.8s | $1.33 | 50/54 |

### flask

> Multi-step Flask refactoring: trace WSGI dispatch, locate tests, add a debug parameter, verify the change. Tests call graph traversal, test-file mapping, and safe code modification awareness.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.848 | 0.748 | 100% | 0.85 | 0.68 | 6,255 | 72.2s | $0.47 | 50/51 |
| 2 | probe :2nd_place_medal: | 0.822 | 0.199 | 95% | 0.83 | 0.60 | 7,521 | 93.9s | $0.62 | 11/11 |
| 3 | gitnexus :3rd_place_medal: | 0.819 | 0.340 | 97% | 0.86 | 0.48 | 9,188 | 133.7s | $0.72 | 14/14 |
| 4 | serena | 0.790 | 0.399 | 78% | 0.81 | 0.59 | 7,645 | 97.6s | $0.51 | 11/11 |
| 5 | baseline | 0.770 | 0.060 | 95% | 0.86 | 0.62 | 7,121 | 89.1s | $0.45 | 9/17 |

### gin

> Multi-step Gin exploration: understand middleware chaining, trace HTTP dispatch, find dead code, modify the recovery middleware. Tests data flow tracing, dead code detection, and structural editing awareness.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.807 | 0.713 | 89% | 0.80 | 0.64 | 7,148 | 80.5s | $0.66 | 52/52 |
| 2 | baseline :2nd_place_medal: | 0.740 | 0.200 | 92% | 0.78 | 0.35 | 11,925 | 163.8s | $0.93 | 75/75 |
| 3 | serena :3rd_place_medal: | 0.732 | 0.663 | 86% | 0.82 | 0.22 | 16,832 | 182.2s | $1.54 | 52/52 |
| 4 | probe | 0.725 | 0.140 | 89% | 0.76 | 0.33 | 12,439 | 161.6s | $0.94 | 58/58 |
| 5 | gitnexus | 0.689 | 0.351 | 94% | 0.75 | 0.15 | 17,103 | 224.4s | $1.38 | 71/71 |

### javalin

> Multi-step Javalin exploration: understand servlet dispatch, trace routing table construction, add a custom error handler. Tests Java framework comprehension and handler registration patterns.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.754 | 0.840 | 100% | 0.89 | 0.38 | 13,306 | 170.6s | $1.08 | 23/38 |
| 2 | probe :2nd_place_medal: | 0.739 | 0.400 | 100% | 0.86 | 0.50 | 10,691 | 139.7s | $0.86 | 28/65 |
| 3 | baseline :3rd_place_medal: | 0.703 | 0.400 | 100% | 0.89 | 0.33 | 13,801 | 198.2s | $1.26 | 19/57 |
| 4 | gitnexus | 0.653 | 0.369 | 100% | 0.86 | 0.39 | 12,674 | 177.2s | $1.15 | 0/6 |
| 5 | serena | 0.621 | 0.614 | 100% | 0.82 | 0.25 | 18,576 | 243.9s | $1.48 | 11/88 |

### nextjs

> Multi-step Next.js exploration: trace SSR render path, understand route matching, thread a request ID. Tests TypeScript monorepo navigation and complex server-side pipeline understanding.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.809 | 0.946 | 88% | 0.85 | 0.75 | 11,326 | 161.4s | $1.18 | 31/44 |
| 2 | baseline :2nd_place_medal: | 0.803 | 0.200 | 91% | 0.80 | 0.63 | 15,270 | 252.0s | $2.19 | 23/24 |
| 3 | serena :3rd_place_medal: | 0.781 | 0.432 | 97% | 0.85 | 0.70 | 13,322 | 194.4s | $1.42 | 18/35 |
| 4 | probe | 0.761 | 0.400 | 91% | 0.86 | 0.71 | 13,602 | 171.8s | $1.26 | 15/40 |
| 5 | gitnexus | 0.736 | 0.780 | 91% | 0.83 | 0.69 | 14,379 | 188.7s | $1.40 | 9/26 |

### Aggregate

Failed runs count as fairness 0 in the average. The `Failures` column shows how many scenarios the tool could not complete. Costs marked with `*` are estimated from per-message token usage in the partial transcript, because the session never emitted a final cost event.

| Rank | Tool | Scenarios | Failures | Avg Fairness | Avg Adoption | Avg Keyword Cov. | Avg LLM Quality | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|------------:|-----------:|---------------:|---------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | sense :1st_place_medal: | 6 | 0 | 0.8132 | 0.7521 | 0.9570 | 0.8542 | 0.5812 | 10,896 | 141.5s | $6.22 | 89.2% (257/288) |
| 2 | probe :2nd_place_medal: | 6 | 0 | 0.7766 | 0.2964 | 0.9284 | 0.8479 | 0.5219 | 12,119 | 162.7s | $6.23 | 72.8% (179/246) |
| 3 | baseline :3rd_place_medal: | 6 | 0 | 0.7715 | 0.2767 | 0.9379 | 0.8417 | 0.4904 | 12,716 | 185.4s | $7.57 | 80.8% (236/292) |
| 4 | serena | 6 | 0 | 0.7520 | 0.5444 | 0.9258 | 0.8336 | 0.4384 | 14,800 | 191.4s | $7.57 | 61.9% (159/257) |
| 5 | gitnexus | 6 | 0 | 0.7492 | 0.4722 | 0.9369 | 0.8452 | 0.4771 | 12,964 | 173.8s | $6.87 | 76.9% (190/247) |
