# Judge calibration — pitch 20-05

First end-to-end run of the LLM-as-judge layer against the 12 transcripts already in `bench/results/`. Goal: validate that the new fairness formula (10/55/15/20) tells a sensible story about who is better at producing AI-agent-grade maps.

> **Note on the hard cut.** Pitch 20-05 rephrased the step prompts in the AI-agent voice. The transcripts under `bench/results/` were generated with the old human-researcher prompts, so the calibration below is the LLM judge layered on top of the *previous* answers. Once tools are re-run against the new prompts, both keyword_coverage and llm_quality will move. The calibration values here are the floor signal, not a final ranking.

## Per-scenario table

```
tool       repo         KWcov   LLMQ  Cite   Eff   Fair
baseline   axum         90.91%  0.781  0.87  0.48  0.747
baseline   discourse    97.06%  0.698  1.00  0.45  0.720
baseline   flask        89.74%  0.774  1.00  0.28  0.722
baseline   gin          FAILED
baseline   javalin     100.00%  0.758  0.80  0.34  0.705
baseline   nextjs       FAILED
sense      axum         96.97%  0.799  0.95  0.67  0.813
sense      discourse    94.12%  0.756  0.73  0.66  0.751
sense      flask        89.74%  0.765  0.60  0.63  0.726
sense      gin          91.18%  0.729  1.00  0.56  0.754
sense      javalin     100.00%  0.804  1.00  0.59  0.811
sense      nextjs       91.18%  0.731  0.25  0.66  0.662
```

## Quality vs keyword coverage

Hand-plotted ranges (LLM quality on Y, keyword coverage on X):

```
LLMQ
 0.81 │                 sense/javalin (100% KW)  sense/axum (97% KW)
 0.80 │
 0.79 │      sense/axum/keyword-ish band ───────────────────────────
 0.78 │ baseline/axum (91%)
 0.77 │ baseline/flask (90%), sense/flask (90%)
 0.76 │ sense/discourse (94%), baseline/javalin (100%)
 0.75 │
 0.74 │
 0.73 │ sense/gin (91%), sense/nextjs (91%)
 0.72 │
 0.71 │
 0.70 │ baseline/discourse (97%) ← high KW, low LLMQ
 0.69 │
       └────────────────────────────────────────────────────────────
         88%      92%      96%     100%        keyword_coverage
```

**Read:** keyword coverage saturates at the top of the range (90-100% on every successful run). Within that flat zone, LLM quality spreads from 0.70 to 0.80. The judge does add signal — `baseline/discourse` lands second-highest on keyword coverage (97%) but second-lowest on LLM quality (0.70), and `sense/gin`/`sense/nextjs` both land around 0.73 LLMQ despite middling 91% keyword coverage. If the judge were redundant we'd see a tight diagonal; we see scatter, so 0.55 weight is earning its keep.

## Hand spot-checks (4 transcripts)

I read each judge rationale alongside the underlying answer for these four. Notes on whether the judge's reasoning is grounded in the answer text or hallucinating about it.

### sense/axum (scenario_quality 0.799)
- Step 1 (Handler impls) gets 0.86. Rationale cites `macros.rs:49-68 all_the_tuples!` and the IntoResponseHandler explanation — those are real artefacts in the answer.
- Step 3 (lifecycle) drops to 0.75 because `uncertainty` is 0.3: "doesn't flag axum-core vs axum split". Skimming the answer confirms it talks Tower internals but doesn't caveat which crate owns what — fair call.
- **Verdict:** judge reads the answer.

### sense/nextjs (scenario_quality 0.731)
- Step 4 gets 0.79 with `map_quality` 0.85 but `specificity` 0.75 because "it doesn't cite NEXT_REQUEST_ID_HEADER (the existing constant)". The keyword check for that constant **is** required in the scenario yaml — and the answer indeed misses it. The judge caught the same gap the keyword check did, independently.
- The low `citation_grounding` (0.25) is what pulled fairness down. Looking at the answer, Sense cites lots of `file:line` references that the grounding checker couldn't resolve — likely because the answer cites paths that exist in a different Next.js commit. Worth re-checking that the citation grounder is using the right `repo_commit`.
- **Verdict:** judge reads the answer; citation_grounding rate is the noisy component here.

### baseline/discourse (scenario_quality 0.698)
- Step 1 gets 0.60: "Confident throughout; no flags on plugin-affected paths" — the answer indeed presents Discourse architecture as a single linear stack, glossing over the plugin extension model.
- Step 4 gets 0.83 (high). Rationale says "comprehensive edit map with file:line, cascading effects called out as 'unintentional consequences'". The judge is being generous here but defensibly — the answer does enumerate a lot of cascading impacts.
- **Verdict:** the within-scenario variance (0.60 to 0.83 across steps) tracks real differences in step quality, not noise.

### baseline/javalin (scenario_quality 0.758)
- Step 4 gets 0.85 — `justification` is 1.0 because "Clearly explains the two-system distinction (ExceptionMapper vs ErrorMapper)". That's a real, well-explained piece of the answer.
- Step 3 (route registration) gets 0.74: justification only 0.6 because "explains the chain mechanically and notes PathMatcher validates duplicates but does not really explain WHY routes go through a particular internal API". Looking at the answer, this is accurate — the trace is good but the framing is "what happens" not "why".
- **Verdict:** judge distinguishes "good map" from "good map plus rationale" reliably.

**Headline from spot-checks:** the judge isn't a rubber stamp. It scores answers carefully against the rubric questions and surfaces gaps a keyword check can't.

## Variance baseline numbers

Full table lives in [`judge-variance.md`](judge-variance.md). Summary:

| Layer | Max stdev (n=2) | Target | Pass? |
|-------|----------------:|-------:|:-----:|
| Per-criterion (raw 0.0–1.0 scores) | 0.071 | 0.05 | ✗ |
| Per-step `step_quality` (weighted sum) | 0.048 | 0.05 | ✓ |
| Per-scenario `scenario_quality` (mean) | max \|Δ\| 0.014 | 0.05 | ✓ |

**Headline:** per-criterion scores are jittery (max stdev 0.071, mostly driven by `uncertainty` on its 0.1-0.4 anchor range). The composite `step_quality` averages that down within target. The scenario-level number — the one that feeds fairness — is rock-solid.

## Rationale for keeping the 10/55/15/20 weights

Argument for each weight, given what calibration showed:

- **keyword_coverage = 0.10.** Saturated at 90-100% on every successful run; barely discriminating. Anything higher than 10% would amplify a signal the new prompt voice will further compress. Keep at 0.10 as a smoke test that the answer is on-topic.
- **llm_quality = 0.55.** Spread of 0.70-0.80 across runs that scored identically on keyword coverage. The judge adds the strongest discriminating signal; 55% reflects that.
- **citation_grounding = 0.15.** Real signal, but volatile (`sense/nextjs` 0.25 while everyone else 0.60+). Two failure modes show up: tools that print *more* file:line citations have more chances to miss; grounding might be checking the wrong `repo_commit` for some runs. Keep at 0.15 — the signal is too useful to drop and capping its weight bounds the volatility. Worth a follow-up to verify grounding's repo-checkout selection.
- **efficiency = 0.20.** Sense's clearest win (0.56-0.67 vs baseline 0.28-0.48). 20% is enough to reward "code-intel saved time" without letting a 30-second answer beat a 90-second better answer.

**No change to the weights.** Revisit after the first re-run with the AI-agent-voice prompts.

## Open follow-ups

- `citation_grounding` for some Sense runs is suspiciously low (sense/nextjs 0.25). Verify the grounding step is reading from the correct `repo_commit` checkout, not a drift between bench commit and answer commit.
- The judge's `uncertainty` criterion is jittery on the low end (0.1-0.4 range). Consider snapping to a coarser scale {0.0, 0.25, 0.5, 0.75, 1.0} for stability — deferred to 20-07.
- Re-run all 6 scenarios with the new AI-agent-voice prompts; expect keyword_coverage to drop (different vocabulary triggers) and llm_quality to either rise (better-shaped prompt → better answers) or stay flat (prompt rephrasing doesn't shift what tools can do, only how it's measured).
