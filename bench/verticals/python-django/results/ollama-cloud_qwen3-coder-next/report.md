## Scenario Evaluation

Results: 19 tools × 12 scenarios

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

### baseline

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| _backup_healthchecks_session2_20260710 | 95% (19/20) | 95% (19/20) | 691,145 | 680,607 | — | — |
| _backup_litellm_session2_20260710 | 74% (17/23) | 74% (17/23) | 1,419,281 | 1,404,087 | — | — |
| _backup_saleor_session2_20260710 | 92% (12/13) | 92% (12/13) | 759,365 | 745,495 | — | — |
| _backup_wagtail_3102cell_20260710 | 8% (1/13) | 0% (0/13) | 1,874,091 | 1,853,690 | — | — |
| _backup_wagtail_degraded_window_20260710 | 85% (11/13) | 85% (11/13) | 744,467 | 730,871 | — | — |
| _invalid_netbox_20260710 | 4% (1/23) | 0% (0/23) | 673,358 | 670,238 | — | — |
| _invalid_sentry_20260709_attempt3 | 6% (1/17) | 6% (1/17) | 519,193 | 516,442 | — | — |
| _invalid_wagtail_3102cell_20260710 | 0% (0/13) | 0% (0/13) | 626,922 | 623,520 | — | — |
| _invalid_wagtail_run1_20260709 | 0% (0/13) | 0% (0/13) | 360,469 | 356,904 | — | — |
| _invalid_wagtail_run1_20260710 | 31% (4/13) | 15% (2/13) | — | — | — | 1117s |
| _invalid_wagtail_run1_20260710b | 0% (0/13) | 0% (0/13) | 137,647 | 136,578 | — | — |
| _invalid_wagtail_run1_20260710c | 0% (0/13) | 0% (0/13) | 632,445 | 629,982 | — | — |

### baseline-sentry

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| _invalid_sentry_20260709 | 0% (0/17) | 0% (0/17) | 77,758 | 77,330 | — | — |
| _invalid_sentry_20260709_attempt2 | 6% (1/17) | 0% (0/17) | 492,559 | 479,497 | — | — |

### healthchecks

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 95% (19/20) | 95% (19/20) | 537,104 | 525,813 | — | — |
| sense | 95% (19/20) | 95% (19/20) | 806,036 | 797,870 | — | — |

_Billed-context Δ (sense vs baseline): **+50%** — Sense loads more._

### litellm

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 30% (7/23) | 4% (1/23) | 1,124,934 | 1,120,357 | — | — |
| sense | 30% (7/23) | 30% (7/23) | 1,114,296 | 1,101,211 | — | — |

_Billed-context Δ (sense vs baseline): **-1%** — Sense loads less._

### netbox

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 74% (17/23) | 70% (16/23) | 970,573 | 953,520 | — | — |
| sense | 78% (18/23) | 78% (18/23) | 794,010 | 783,590 | — | — |

_Billed-context Δ (sense vs baseline): **-18%** — Sense loads less._

### run-1

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| _invalid_saleor_baseline_run1_20260710 | 0% (0/13) | 0% (0/13) | 512,113 | 509,825 | — | — |

### run-2

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| _invalid_saleor_sense_run2_20260710 | 0% (0/13) | 0% (0/13) | 279,214 | 276,973 | — | — |
| _invalid_sentry_sense_run2_20260710 | 6% (1/17) | 0% (0/17) | 183,246 | 182,525 | — | — |

### saleor

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 77% (10/13) | 77% (10/13) | 681,766 | 673,203 | — | — |
| sense | 69% (9/13) | 69% (9/13) | 884,919 | 874,032 | — | — |

_Billed-context Δ (sense vs baseline): **+30%** — Sense loads more._

### sense

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| _backup_healthchecks_session2_20260710 | 100% (20/20) | 100% (20/20) | 503,737 | 493,849 | — | — |
| _backup_litellm_session2_20260710 | 30% (7/23) | 30% (7/23) | 1,605,415 | 1,590,378 | — | — |
| _backup_saleor_session2_20260710 | 54% (7/13) | 54% (7/13) | 575,719 | 567,289 | — | — |
| _backup_wagtail_3102cell_20260710 | 100% (13/13) | 100% (13/13) | 1,922,109 | 1,886,241 | — | — |
| _backup_wagtail_degraded_window_20260710 | 15% (2/13) | 8% (1/13) | 559,689 | 539,237 | — | — |
| _invalid_netbox_20260710 | 0% (0/23) | 0% (0/23) | 1,371,257 | 1,365,936 | — | — |
| _invalid_sentry_20260709_attempt3 | 0% (0/17) | 0% (0/17) | 580,038 | 576,861 | — | — |
| _invalid_wagtail_3102cell_20260710 | 92% (12/13) | 92% (12/13) | 680,366 | 657,256 | — | — |
| _invalid_wagtail_run1_20260709 | 8% (1/13) | 8% (1/13) | 840,529 | 836,916 | — | — |
| _invalid_wagtail_run1_20260710 | 0% (0/13) | 0% (0/13) | 235,862 | 235,260 | — | — |
| _invalid_wagtail_run1_20260710b | 0% (0/13) | 0% (0/13) | 1,247,180 | 1,243,733 | — | — |
| _invalid_wagtail_run1_20260710c | 0% (0/13) | 0% (0/13) | 1,311,112 | 1,307,730 | — | — |

### sense-sentry

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| _invalid_sentry_20260709 | 0% (0/17) | 0% (0/17) | 1,714,607 | 1,710,194 | — | — |
| _invalid_sentry_20260709_attempt2 | 0% (0/17) | 0% (0/17) | 405,355 | 403,690 | — | — |

### sentry

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 47% (8/17) | 47% (8/17) | 434,101 | 420,468 | — | — |
| sense | 41% (7/17) | 41% (7/17) | 1,880,619 | 1,868,447 | — | — |

_Billed-context Δ (sense vs baseline): **+333%** — Sense loads more._

### wagtail

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| baseline | 69% (9/13) | 69% (9/13) | 5,590,178 | 5,558,940 | — | — |
| sense | 92% (12/13) | 92% (12/13) | 1,197,777 | 1,189,296 | — | — |

_Billed-context Δ (sense vs baseline): **-79%** — Sense loads less._

### Aggregate

Ranked by **cited recall** (the headline). **B-score** = `0.55·cited + 0.25·correct-relationship rate + 0.20·truthfulness`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial token usage.

| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | _backup_healthchecks_session2_20260710 :1st_place_medal: | 4 | 0 | 0.9500 | **0.9516** | 0.9625 | 0.9375 | 0.9737 | **2** | 0.0000 | 673,383 | 0.0s | — | 99.4% (174/175) |
| 2 | _backup_saleor_session2_20260710 :2nd_place_medal: | 4 | 0 | 0.8077 | **0.8317** | 0.8077 | 0.7500 | 1.0000 | 0 | 0.0000 | 860,152 | 0.0s | — | 99.0% (582/588) **!5** |
| 3 | sense :3rd_place_medal: | 12 | 0 | 0.7791 | **0.8137** | 0.7632 | 0.7440 | 0.9960 | **1** | 0.0000 | 1,164,753 | 0.0s | — | 97.5% (1647/1689) **!26** |
| 4 | baseline | 12 | 0 | 0.6972 | **0.7411** | 0.7200 | 0.6489 | 0.9772 | **5** | 0.0000 | 1,351,058 | 0.0s | — | 74.3% (1329/1789) **!82** |
| 5 | _backup_wagtail_3102cell_20260710 | 3 | 0 | 0.6154 | **0.7115** | 0.7180 | 0.6923 | 1.0000 | 0 | 0.0000 | 1,998,528 | 0.0s | — | 95.1% (429/451) **!16** |
| 6 | _backup_wagtail_degraded_window_20260710 | 4 | 0 | 0.5577 | **0.6510** | 0.6731 | 0.5769 | 1.0000 | 0 | 0.0000 | 699,204 | 0.0s | — | 87.5% (885/1012) **!38** |
| 7 | _backup_litellm_session2_20260710 | 4 | 0 | 0.4347 | **0.5220** | 0.4348 | 0.3913 | 0.9254 | **3** | 0.0000 | 1,194,690 | 0.0s | — | 94.3% (331/351) |
| 8 | _invalid_wagtail_3102cell_20260710 | 4 | 0 | 0.2308 | **0.3798** | 0.2308 | 0.2115 | 1.0000 | 0 | 0.0000 | 580,828 | 0.0s | — | 72.8% (402/552) **!7** |
| 9 | _invalid_sentry_20260709 | 4 | 0 | 0.1912 | **0.3493** | 0.1912 | 0.1765 | 1.0000 | 0 | 0.0000 | 612,010 | 0.0s | — | 97.2% (205/211) |
| 10 | _invalid_sentry_20260709_attempt3 | 4 | 0 | 0.1323 | **0.3058** | 0.1470 | 0.1323 | 1.0000 | 0 | 0.0000 | 620,156 | 0.0s | — | 97.7% (167/171) |
| 11 | _invalid_netbox_20260710 | 4 | 0 | 0.1196 | **0.3066** | 0.1848 | 0.1631 | 1.0000 | 0 | 0.0000 | 1,001,920 | 0.0s | — | 79.0% (15/19) **!4** |
| 12 | _invalid_wagtail_run1_20260710 | 2 | 0 | 0.0769 | **0.3000** | 0.3077 | 0.2308 | 1.0000 | 0 | 0.0000 | 117,931 | 558.5s | — | 100.0% (6/6) |
| 13 | _invalid_wagtail_run1_20260709 | 4 | 0 | 0.0192 | **0.2202** | 0.0384 | 0.0384 | 1.0000 | 0 | 0.0000 | 366,610 | 0.0s | — | 75.0% (3/4) |
| 14 | _invalid_sentry_20260709_attempt2 | 4 | 0 | 0.0147 | **0.2228** | 0.0588 | 0.0588 | 1.0000 | 0 | 0.0000 | 738,578 | 0.0s | — | 100.0% (17/17) |
| 15 | _invalid_saleor_baseline_run1_20260710 | 1 | 0 | 0.0000 | **0.2000** | 0.0769 | 0.0000 | 1.0000 | 0 | 0.0000 | 512,113 | 0.0s | — | — |
| 16 | _invalid_saleor_sense_run2_20260710 | 1 | 0 | 0.0000 | **0.2192** | 0.0769 | 0.0769 | 1.0000 | 0 | 0.0000 | 279,214 | 0.0s | — | — |
| 17 | _invalid_sentry_sense_run2_20260710 | 1 | 0 | 0.0000 | **0.2147** | 0.0588 | 0.0588 | 1.0000 | 0 | 0.0000 | 183,246 | 0.0s | — | — |
| 18 | _invalid_wagtail_run1_20260710b | 2 | 0 | 0.0000 | — | 0.0000 | 0.0000 | — | 0 | 0.0000 | 692,414 | 0.0s | — | — |
| 19 | _invalid_wagtail_run1_20260710c | 2 | 0 | 0.0000 | — | 0.0000 | 0.0000 | — | 0 | 0.0000 | 971,778 | 0.0s | — | — |


### Process efficiency (at held recall)

_Sense recall is HIGHER (0.78 vs 0.70) — any process saving is a bonus on top of a completeness win._

| Process axis | baseline | sense | Δ |
|------|---------:|------:|----:|
| Reads | 17 | 17 | **+2%** |
| Tool calls | 56 | 43 | **-23%** |
| Billed tokens | 1,351,058 | 1,164,753 | **-14%** |
