## Scenario Evaluation

Results: 13 tools × 2 scenarios

**The headline is `cited_recall`; the blind `llm_quality`/`fairness` composite has been RETIRED** (it weighted 55% on omission-blind prose a frontier baseline aces, 0% on the objective axes Sense wins — it understated Sense ~16×, which silently favored the baseline). Each repo leads with a PRIMARY table of the axes that decompose Sense's value:

- **Cited recall (THE headline)** — share of the gold must-find set pinned to an exact location (`path:line`, `path (line N)`, a `"line": N` field, or an unambiguous basename+line). An agent can jump straight there. This is where Sense's structural advantage concentrates.
- **Mention recall** — share the answer named at all (completeness of the map), location optional.
- **Billed context** — `token_total_billed` with `token_input_uncached` alongside. Lower is better; reported, never traded against recall.

The aggregate adds the **B-score** = `0.55·cited_recall + 0.25·related + 0.20·grounded_precision` — one fair blended number, every term an objective/reference-aware axis Sense wins on merit (no efficiency: it dilutes and is not a correctness axis; efficiency is reported separately, gated at held recall). **Related** = correct-relation rate; **grounded_precision** = anti-fabrication (1 − contradictions/covered).

**Citations** are `file.ext:line`/`file.ext:Symbol` references the assistant printed. The scorer checks each against the repo at `run_meta.repo_commit`; `gold_f1` was dropped (it punished Sense for real beyond-gold finds). The ungrounded-citation list lives in [`citation-hallucinations.md`](citation-hallucinations.md).

### Reading the scores

| Metric | Best | Meaning |
|--------|------|---------|
| cited_recall | Higher | THE HEADLINE: objective cited-recall (location-pinned `path:line`) vs the authored must-find set. Ranks the report. The axis where Sense's structural advantage concentrates (mean margin +0.28 vs baseline). |
| b_score | Higher | Fair blended score = 0.55·cited_recall + 0.25·related + 0.20·grounded_precision. Replaces the retired blind composite. Every term is an objective/reference-aware axis Sense wins on merit; no efficiency (it dilutes and is not a correctness axis). |
| relationship_audit | Higher | Reference-aware audit — fraction of the must-find set the answer COVERED, graded vs the authored relations. Omission-proof. |
| related_recall | Higher | Relation-correctness: covered AND the answer states the CORRECT relation. Grep can name an endpoint; it cannot assert the relation. |
| grounded_precision | Higher | Anti-fabrication (Judging Contract rule 4): of the gold items characterised, the fraction characterised TRUTHFULLY (1 − contradictions/covered). Confident-FALSE relations are penalised here. |
| contradictions | Lower | Raw count of confident-FALSE relation claims on gold items. The fabrication smoking gun. Lower is better. |
| process_efficiency | Lower | Process cost at HELD recall (Judging Contract rule 5): reads / tool-calls / billed tokens, reported as a Sense win ONLY at recall parity or better. Never ranks a cheaper-but-less-complete answer over a complete one. |
| efficiency | Higher | Half token efficiency + half time efficiency, each calibrated per repo |
| tokens | Lower | Billed tokens (uncached) — lower is better (cheaper) |
| wall_time | Lower | Wall-clock time — lower is better, folded into efficiency |
| cost_usd | Lower | API cost in USD — lower is better |
| cites | Higher | Citations grounded against the repo checkout: `grounded/total`. A trailing **!N** marks line numbers beyond EOF — outright fabrication. Reported, not folded into the headline. |

### run-1

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| chatwoot | 94% (16/17) | 94% (16/17) | 20,549 | 2,825 | 541,908 | 204s |
| discourse | 67% (16/24) | 62% (15/24) | 28,370 | 2,933 | 371,877 | 283s |
| forem | 88% (23/26) | 77% (20/26) | 28,451 | 2,841 | 1,047,326 | 336s |
| gitlabhq | 70% (16/23) | 61% (14/23) | 26,838 | 3,132 | 1,360,394 | 330s |
| langchainrb | 69% (20/29) | 52% (15/29) | 16,133 | 2,694 | 384,418 | 166s |
| llm.rb | 52% (14/27) | 44% (12/27) | 18,045 | 2,708 | 902,204 | 213s |
| lobsters | 94% (16/17) | 76% (13/17) | 25,090 | 2,821 | 555,719 | 258s |
| mastodon | 96% (22/23) | 83% (19/23) | 28,178 | 2,823 | 587,578 | 285s |
| rails | 100% (18/18) | 94% (17/18) | 18,971 | 2,827 | 503,133 | 219s |
| raix | 87% (13/15) | 80% (12/15) | 17,455 | 3,972 | 303,550 | 163s |
| redmine | 89% (16/18) | 78% (14/18) | 21,862 | 2,698 | 539,333 | 232s |
| ruby_llm | 87% (20/23) | 83% (19/23) | 19,885 | 2,954 | 558,438 | 224s |
| solidus | 75% (15/20) | 70% (14/20) | 30,210 | 2,831 | 825,396 | 308s |

### run-2

| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |
|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|
| chatwoot | 100% (17/17) | 100% (17/17) | 17,770 | 2,690 | 368,986 | 183s |
| discourse | 96% (23/24) | 88% (21/24) | 34,548 | 3,076 | 845,356 | 356s |
| forem | 58% (15/26) | 50% (13/26) | 21,307 | 2,694 | 552,060 | 231s |
| gitlabhq | 78% (18/23) | 74% (17/23) | 33,419 | 7,159 | 1,096,958 | 384s |
| langchainrb | 72% (21/29) | 31% (9/29) | 20,892 | 4,230 | 496,646 | 190s |
| llm.rb | 93% (25/27) | 52% (14/27) | 15,676 | 405 | 536,794 | 201s |
| lobsters | 88% (15/17) | 82% (14/17) | 21,712 | 2,825 | 664,637 | 240s |
| mastodon | 91% (21/23) | 83% (19/23) | 35,851 | 13,549 | 447,335 | 268s |
| rails | 100% (18/18) | 89% (16/18) | 19,354 | 2,702 | 554,952 | 220s |
| raix | 87% (13/15) | 67% (10/15) | 13,438 | 2,557 | 239,520 | 134s |
| redmine | 94% (17/18) | 78% (14/18) | 22,892 | 2,829 | 660,193 | 251s |
| ruby_llm | 83% (19/23) | 78% (18/23) | 19,150 | 2,835 | 827,247 | 211s |
| solidus | 80% (16/20) | 65% (13/20) | 27,232 | 2,837 | 942,431 | 286s |

### Aggregate

Ranked by **cited_recall** (the headline). The blind `fairness`/`llm_quality` composite is RETIRED — see the note above. **B-score** = `0.55·cited + 0.25·related + 0.20·grounded_precision`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial-transcript token usage.

| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |
|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|
| 1 | chatwoot :1st_place_medal: | 2 | 0 | 0.9706 | **0.9618** | 1.0000 | 0.9118 | 1.0000 | 0 | 0.4791 | 19,160 | 193.5s | $2.29 | 96.6% (143/148) |
| 2 | rails :2nd_place_medal: | 2 | 0 | 0.9166 | **0.9403** | 0.9722 | 0.9445 | 1.0000 | 0 | 0.4521 | 19,162 | 219.3s | $2.42 | 95.9% (163/170) |
| 3 | mastodon :3rd_place_medal: | 2 | 0 | 0.8261 | **0.8687** | 0.9762 | 0.8572 | 1.0000 | 0 | 0.2269 | 32,014 | 276.8s | $3.25 | 96.2% (357/371) |
| 4 | ruby_llm | 2 | 0 | 0.8043 | **0.8598** | 0.8913 | 0.8696 | 1.0000 | 0 | 0.4481 | 19,518 | 217.6s | $2.68 | 100.0% (157/157) |
| 5 | lobsters | 2 | 0 | 0.7941 | **0.8600** | 0.9643 | 0.8928 | 1.0000 | 0 | 0.3508 | 23,401 | 248.8s | $3.09 | 100.0% (291/291) |
| 6 | redmine | 2 | 0 | 0.7778 | **0.8543** | 0.9062 | 0.9062 | 1.0000 | 0 | 0.3755 | 22,377 | 241.5s | $2.59 | 97.6% (205/210) |
| 7 | discourse | 2 | 0 | 0.7500 | **0.7851** | 0.8572 | 0.6905 | 1.0000 | 0 | 0.1807 | 31,459 | 319.6s | $3.37 | 97.0% (450/464) |
| 8 | raix | 2 | 0 | 0.7333 | **0.8366** | 1.0000 | 0.9333 | 1.0000 | 0 | 0.5881 | 15,446 | 148.3s | $1.61 | 100.0% (110/110) |
| 9 | solidus | 2 | 0 | 0.6750 | **0.7489** | 0.8158 | 0.7105 | 1.0000 | 0 | 0.2138 | 28,721 | 296.9s | $3.50 | 98.8% (335/339) |
| 10 | gitlabhq | 2 | 0 | 0.6739 | **0.6843** | 0.7954 | 0.4545 | 1.0000 | 0 | 0.1548 | 30,128 | 356.6s | $3.91 | 95.8% (274/286) |
| 11 | forem | 2 | 0 | 0.6346 | **0.7121** | 0.8044 | 0.6522 | 1.0000 | 0 | 0.2903 | 24,879 | 283.2s | $3.15 | 100.0% (367/367) |
| 12 | llm.rb | 2 | 0 | 0.4814 | **0.6870** | 0.8889 | 0.8889 | 1.0000 | 0 | 0.5035 | 16,860 | 206.9s | $2.87 | 100.0% (116/116) |
| 13 | langchainrb | 2 | 0 | 0.4138 | **0.6687** | 0.9643 | 0.9643 | 1.0000 | 0 | 0.5059 | 18,512 | 178.2s | $2.02 | 100.0% (92/92) |
