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
| 1 | gitnexus :1st_place_medal: | 0.845 | 0.486 | 100% | 0.90 | 0.50 | 11,965 | 192.8s | $1.52 | 35/35 |
| 2 | sense :2nd_place_medal: | 0.844 | 0.600 | 100% | 0.90 | 0.49 | 12,872 | 180.7s | $1.40 | 89/89 |
| 3 | serena :3rd_place_medal: | 0.830 | 0.675 | 100% | 0.91 | 0.40 | 15,247 | 210.9s | $1.21 | 65/65 |
| 4 | probe | 0.815 | 0.486 | 97% | 0.89 | 0.40 | 14,605 | 229.8s | $1.69 | 66/67 |
| 5 | baseline | 0.795 | 0.400 | 100% | 0.88 | 0.31 | 16,742 | 257.6s | $1.68 | 93/93 |

### discourse

> Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.827 | 0.622 | 97% | 0.88 | 0.48 | 16,872 | 228.1s | $1.20 | 59/59 |
| 2 | gitnexus :2nd_place_medal: | 0.825 | 0.421 | 82% | 0.90 | 0.48 | 17,144 | 225.1s | $1.26 | 48/48 |
| 3 | baseline :3rd_place_medal: | 0.824 | 0.400 | 85% | 0.91 | 0.44 | 15,873 | 283.2s | $1.97 | 61/61 |
| 4 | serena | 0.821 | 0.400 | 85% | 0.91 | 0.43 | 18,148 | 257.6s | $1.27 | 38/38 |
| 5 | probe | 0.805 | 0.400 | 88% | 0.88 | 0.43 | 18,789 | 250.2s | $1.39 | 85/85 |

### flask

> Multi-step Flask refactoring: trace WSGI dispatch, locate tests, add a debug parameter, verify the change. Tests call graph traversal, test-file mapping, and safe code modification awareness.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.853 | 0.680 | 100% | 0.90 | 0.55 | 8,517 | 106.6s | $0.51 | 26/26 |
| 2 | serena :2nd_place_medal: | 0.844 | 0.373 | 92% | 0.89 | 0.57 | 7,885 | 109.5s | $0.59 | 12/12 |
| 3 | baseline :3rd_place_medal: | 0.842 | 0.060 | 95% | 0.87 | 0.60 | 7,368 | 96.5s | $0.47 | 6/6 |
| 4 | gitnexus | 0.835 | 0.509 | 95% | 0.85 | 0.64 | 6,917 | 83.5s | $0.54 | 18/19 (**!1**) |
| 5 | probe | 0.801 | 0.166 | 97% | 0.85 | 0.44 | 10,723 | 128.8s | $0.64 | 12/12 |

### gin

> Multi-step Gin exploration: understand middleware chaining, trace HTTP dispatch, find dead code, modify the recovery middleware. Tests data flow tracing, dead code detection, and structural editing awareness.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.791 | 0.750 | 97% | 0.86 | 0.35 | 12,295 | 155.0s | $0.80 | 82/82 |
| 2 | gitnexus :2nd_place_medal: | 0.780 | 0.480 | 89% | 0.88 | 0.30 | 12,951 | 169.7s | $0.97 | 41/42 |
| 3 | baseline :3rd_place_medal: | 0.732 | 0.200 | 97% | 0.73 | 0.42 | 7,181 | 219.1s | $1.19 | 65/65 |
| 4 | probe | 0.703 | 0.464 | 92% | 0.75 | 0.25 | 14,004 | 182.4s | $0.96 | 59/59 |
| 5 | serena | 0.123 | 0.502 | 3% | 0.00 | 0.60 | 6,699 | 112.4s | $1.04 | — |

### javalin

> Multi-step Javalin exploration: understand servlet dispatch, trace routing table construction, add a custom error handler. Tests Java framework comprehension and handler registration patterns.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | gitnexus :1st_place_medal: | 0.842 | 0.550 | 100% | 0.91 | 0.46 | 10,925 | 164.8s | $1.22 | 60/60 |
| 2 | serena :2nd_place_medal: | 0.837 | 0.533 | 100% | 0.90 | 0.47 | 8,120 | 251.2s | $1.95 | 49/49 |
| 3 | baseline :3rd_place_medal: | 0.822 | 0.400 | 100% | 0.90 | 0.40 | 12,213 | 188.9s | $1.37 | 62/62 |
| 4 | sense | 0.806 | 0.905 | 100% | 0.90 | 0.30 | 13,960 | 223.0s | $1.22 | 60/60 |
| 5 | probe | 0.786 | 0.400 | 100% | 0.88 | 0.25 | 16,113 | 235.9s | $1.43 | 51/51 |

### nextjs

> Multi-step Next.js exploration: trace SSR render path, understand route matching, thread a request ID. Tests TypeScript monorepo navigation and complex server-side pipeline understanding.

| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |
|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|
| 1 | sense :1st_place_medal: | 0.868 | 0.909 | 100% | 0.86 | 0.73 | 11,733 | 176.3s | $1.21 | 39/39 |
| 2 | baseline :2nd_place_medal: | 0.866 | 0.400 | 88% | 0.89 | 0.68 | 9,072 | 299.6s | $2.25 | 58/58 |
| 3 | probe :3rd_place_medal: | 0.864 | 0.400 | 91% | 0.89 | 0.65 | 9,822 | 322.2s | $2.41 | 63/63 |
| 4 | gitnexus | 0.853 | 0.366 | 91% | 0.89 | 0.62 | 16,867 | 238.2s | $1.65 | 30/30 |
| 5 | serena | 0.835 | 0.918 | 91% | 0.86 | 0.63 | 16,036 | 240.1s | $1.52 | 41/42 |

### Aggregate

Failed runs count as fairness 0 in the average. The `Failures` column shows how many scenarios the tool could not complete. Costs marked with `*` are estimated from per-message token usage in the partial transcript, because the session never emitted a final cost event.

| Rank | Tool | Scenarios | Failures | Avg Fairness | Avg Adoption | Avg Keyword Cov. | Avg LLM Quality | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|------------:|-----------:|---------------:|---------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | sense :1st_place_medal: | 6 | 0 | 0.8318 | 0.7444 | 0.9905 | 0.8838 | 0.4837 | 12,708 | 178.3s | $6.33 | 100.0% (355/355) |
| 2 | gitnexus :2nd_place_medal: | 6 | 0 | 0.8300 | 0.4685 | 0.9284 | 0.8887 | 0.5016 | 12,795 | 179.0s | $7.16 | 99.2% (232/234) **!1** |
| 3 | baseline :3rd_place_medal: | 6 | 0 | 0.8135 | 0.3100 | 0.9422 | 0.8623 | 0.4750 | 11,408 | 224.2s | $8.94 | 100.0% (345/345) |
| 4 | probe | 6 | 0 | 0.7957 | 0.3860 | 0.9421 | 0.8568 | 0.4032 | 14,009 | 224.9s | $8.52 | 99.7% (336/337) |
| 5 | serena | 6 | 0 | 0.7148 | 0.5670 | 0.7852 | 0.7430 | 0.5160 | 12,022 | 197.0s | $7.58 | 99.5% (205/206) |
