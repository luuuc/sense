# bench — Scoring

Single source of truth for how a transcript becomes a number. If this
doc disagrees with `lib/fairness.py` or `lib/scorer.py`, the code wins
— fix the doc. See [`CHANGELOG.md`](./CHANGELOG.md) for the ship
history; this doc is the consolidated "what's true now".

---

## The five dimensions, and how the formula captures them

The bench's job is to score **five dimensions of "did the AI agent
get a useful answer?"**:

| Dimension | What it asks |
|---|---|
| **Hallucinations** | Are file paths, line numbers, symbol attributions correct? |
| **Correctness** | Are call chains, callers/callees, behaviour accurate? |
| **Actionability** | Could the next agent edit from this answer without re-exploring? |
| **Serendipity** | Did the answer surface impacts/sites the user didn't ask about? |
| **Efficiency** | Did this come at reasonable token + wall-time cost? |

The bench started with two axes (`correctness × efficiency`) on the
view that keyword checks couldn't independently measure all five
honestly — *"two honest dimensions, not four fake ones"*. Subsequent
work walked that back by adding **non-keyword measurement** for each
dimension. The current 4-axis fairness formula is the projection of
those five dimensions onto axes the code can actually compute:

| Dimension | Captured by | Notes |
|---|---|---|
| Hallucinations | **citation_grounding** (15%) | Every `file:line` is verified against the repo at `repo_commit`. EOF-overrun = `hallucinated`, missing path = `unresolved`. |
| Correctness | **keyword_coverage** (10%) + **llm_quality.map_quality** | Required keywords cover the must-mention call-chain spine; the judge scores chain-completeness in prose. |
| Actionability | **llm_quality.specificity** + **llm_quality.justification** | Judge rewards `file:line`-level precision and "why this matters", not just naming. |
| Serendipity | **llm_quality.uncertainty** + **adoption.discoverability** | Judge credits flagged-but-unsure surfaces; adoption layer counts unique files cited. |
| Efficiency | **efficiency** (20%) | Half tokens, half wall-time, calibrated per repo. |

Adoption is **never folded into fairness** — a tool isn't penalised
for being a generic agent rather than a code-intel layer. It's
reported alongside for code-intel-vs-code-intel comparisons only
(sense vs roam vs greptile).

---

## Two layers

| Layer | What it answers | Used for |
|---|---|---|
| **Fairness** | Did the tool produce a useful answer for the next AI agent? | sense vs baseline (the headline) |
| **Adoption** | Did the tool get used the way it's meant to be used? | code-intel vs code-intel only |

---

## Fairness formula

```
fairness = 0.10 · keyword_coverage           ← smoke test (was the headline pre-20-05)
         + 0.55 · llm_quality                ← the headline (added 20-05)
         + 0.15 · citation_grounding         ← anti-hallucination (added 20-04)
         + 0.20 · efficiency                 ← tokens + time (calibrated per repo)
```

Defined in [`lib/fairness.py`](./lib/fairness.py). The four axes are
**locked** in [`locked/locked.yaml`](./locked/locked.yaml) — the
improvement loop may re-weight them within ±0.05/iter/axis but cannot
add, remove, or rename them. Renaming would break comparability across
runs and require re-grading the held-out anchor.

If `judged.json` is missing for a result, fairness renders as `—` in
the report (the formula won't run without `llm_quality`).

### Component definitions

#### `keyword_coverage` (10%) — smoke test

Was the obvious vocabulary present? Computed as

```
keyword_coverage = Σ(hits_required + 0.5 · hits_bonus)
                 / Σ(total_required + 0.5 · total_bonus)
```

across every check in the scenario, summed across steps. Bonus checks
are half-weighted. Adoption-layer checks (`mcp_tool_used`, `no_grep`)
are excluded.

Two scoring-engine subtleties:

- Checks search **`answer_text`** only (assistant prose), not the
  tool-call inputs. Pre-20-03, a `Grep("TopicCreator")` invocation
  produced a "hit" for the keyword `TopicCreator` even if grep
  returned nothing — a real exploit surface for any tool that prefers
  grep. Tool inputs/results live in `audit_text` for diagnostics but
  are never scored against.
- `keyword_coverage` is a true hit-rate (Σhits / Σtotal across all
  checks), not a step-mean. Pre-20-03 the step-mean weighted a 1-check
  step the same as a 10-check step. The step-mean is preserved as
  `step_avg_score` for anyone who wants it.

Supported check types ([`lib/scorer.py:evaluate_check`](./lib/scorer.py)):

| Type | Match |
|---|---|
| `contains` / `transcript_contains` | Substring, case-insensitive, **no boundary** — matches inside identifiers |
| `phrase` | Substring with non-word boundaries on both sides — preferred over `contains` for short tokens |
| `word` | Whole-word match (regex word boundary) |
| `starts_with` | Any line in the answer starts with the value |
| `exact` | Verbatim substring (case-sensitive) |
| `mcp_tool_used` | Tool name appears in tool_calls (adoption) |
| `no_grep` | grep was *never* used (adoption) |
| `diff_contains` | Value appears in `git diff` of the result_dir |
| `response_richness` | ≥ N unique source files cited in the answer |

#### `llm_quality` (55%) — the headline

Claude Opus 4.7 acting as judge, scoring each step against the
scenario's rubric (`scenarios/<repo>.rubric.yaml`). Each rubric step
defines four weighted criteria, weights summing to 1.0:

| Criterion | Default weight | What it asks |
|---|---|---|
| `map_quality` | 0.40 | Does the answer give file:line targets, in edit-order, that the agent could act on without re-exploring? |
| `specificity` | 0.25 | Are citations file:line / file:Symbol, not vague file mentions? |
| `justification` | 0.20 | Does the answer explain *why* each cited symbol matters in the flow? |
| `uncertainty` | 0.15 | Where the LLM is uncertain (plugin interactions, edge cases), does it say so? |

```
step_quality      = Σ (criterion_weight · criterion_score)
scenario_quality  = mean(step_quality across steps)
llm_quality       = scenario_quality
```

The judge prompt is locked at
[`lib/judge_prompt.v1.md`](./lib/judge_prompt.v1.md). It instructs
the judge to score for an **AI agent audience** — not a human reader
and not for documentation quality. Per the prompt:

- The **answer text** is the primary input.
- **Side-context** (`wall_time_seconds`, `total_tokens`, `completed`)
  is provided but is **only** to be used for criteria the rubric
  explicitly invites — typically `map_quality` ("did this answer save
  the agent downstream exploration?") and `uncertainty` ("is the
  confidence calibrated against effort spent?"). The judge is told not
  to reward speed/cost in isolation for `specificity` or `justification`.

**Reproducibility tuple:** `{prompt version, model id, scenario rubric,
temperature=0.0}`. The judge model identity is locked in `locked.yaml`
— swapping models invalidates every prior score, so a model change is
a versioned bench event (re-grade held-out + bump `locked_version`).

**Variance** (from the calibration pass on 12 real transcripts —
re-run with [`lib/variance.py`](./lib/variance.py)):

| Layer | Target stdev | Observed | Verdict |
|---|---|---|---|
| Per-criterion score | <0.05 | max 0.071 | **Fails target** — treat as diagnostic |
| Per-step `step_quality` | — | max 0.048 | Acceptable |
| Per-scenario `scenario_quality` | — | max \|Δ\| 0.014 | **Rock-solid for ranking** |

Use `scenario_quality` and `step_quality` for any decision; treat
individual criterion scores as commentary, not data.

**Calibration:** the 10/55/15/20 weights were validated against the
existing 12 transcripts and hand spot-checks before being committed.
Eligible for ±0.05/iter movement by the improvement loop thereafter.

#### `citation_grounding` (15%) — anti-hallucination

Every `file.ext:line` and `file.ext:Symbol` reference in the
**answer_text** is extracted ([`lib/grounding.py`](./lib/grounding.py))
and checked against the repo at `run_meta.repo_commit`:

| Status | Meaning | Counted as |
|---|---|---|
| `grounded` | File exists, line ≤ EOF (or symbol resolves within ±5 lines) | hit |
| `unresolved` | File not at the cited path (typo, basename-only, different dir) | miss |
| `hallucinated` | File exists but line > EOF — outright fabrication | miss + flagged with `!N` in reports |

```
citation_grounding_rate = grounded / total
```

If the answer printed **no** structured citations (`total == 0`), the
component is **0.0**, not 1.0 — *"no map" is not credit-worthy for the
AI-agent audience*. `report.sh` regenerates the full hallucination
list as `citation-hallucinations.md` next to the report.

**Symbol grounding** uses naive ±5 line word-boundary grep, not
tree-sitter — covers ~95% of cases at a fraction of the complexity.

#### `efficiency` (20%) — tokens + time

Half token efficiency, half time efficiency, both calibrated per repo:

```
token_eff   = max(0, 1 − billed_tokens / EFFICIENCY_CEILINGS[repo])
time_eff    = max(0, 1 − wall_time     / TIME_CEILINGS[repo])
efficiency  = 0.5 · token_eff + 0.5 · time_eff
```

`billed_tokens = input_tokens + output_tokens` (uncached only —
cache reads/writes don't count against the tool). Zero tokens or
zero wall-time → zero in that half, not perfect.

Per-repo ceilings live in [`lib/scorer.py`](./lib/scorer.py) and are
tunable by the loop (±20%/iter, audit-justified). Reduced 20% from the
original Card-15 values after observed sessions ran well inside them
(mean 145s, max 257s on iter-1):

| Repo | EFFICIENCY_CEILINGS (tokens) | TIME_CEILINGS (s) | BUDGET_PER_REPO (USD) |
|---|---:|---:|---:|
| flask     | 15,000 | 320 | 1.00 |
| gin       | 15,000 | 320 | 1.00 |
| javalin   | 15,000 | 480 | 1.75 |
| axum      | 20,000 | 480 | 1.75 |
| discourse | 30,000 | 480 | 2.00 |
| nextjs    | 40,000 | 720 | 2.25 |
| *unknown* | 30,000 | 480 | 1.50 |

---

## Adoption formula

```
adoption = 0.60 · tool_fluency + 0.40 · discoverability
```

```
tool_fluency      = mcp_calls / (mcp_calls + grep_calls)   (default 0.5 if neither)
discoverability   = min(1, unique_source_files_cited / 20)
```

`grep_calls` includes `Grep`, `Glob`, raw `grep`, and `rg` — anything
that bypasses the code-intel layer for string search (Glob was
silently uncounted pre-20-03). Reads of `.summary.md` are excluded
from file counts. Discoverability ceiling is 20 (raised from 10 in
20-03) so it stops saturating mid-scenario.

Reported alongside fairness so you can see whether a sense win was
driven by the right reason.

---

## Failure semantics

`scored.json` carries two distinct flags. They are **not** the same:

| Flag | Meaning | Score impact |
|---|---|---|
| `failed: true` | Session crashed — no answer text in the transcript | Everything zeroed; fairness = 0.0 immediately |
| `constrained: true` + `constraint_reason` | Session hit `--max-budget-usd` or `--timeout` but produced answer content | Scored normally; flag is an audit signal only |

A session over budget but with a complete answer is **not** a
failure — its answer is judged like any other. Every downstream
consumer (`fairness.py`, `judge.py`, `audit_*.py`, `reporter.py`)
short-circuits only on `failed`, never on `constrained`.

---

## Reading `report.md`

| Column | Source | Best | Notes |
|---|---|---|---|
| Fairness | `fairness.compute(scored, judged)` | Higher | `—` if `judged.json` missing |
| Adoption | `scored["adoption_score"]` | Higher | Code-intel comparisons only |
| Keyword Cov. | `scored["keyword_coverage"]` | Higher | 10% smoke test |
| LLM Quality | `judged["scenario_quality"]` | Higher | The headline (55%) |
| Efficiency | `scored["efficiency"]` | Higher | Half tokens, half time, per-repo |
| Tokens | `scored["metrics"]["token_total_billed"]` | Lower | Uncached input + output |
| Time | `scored["metrics"]["wall_time_seconds"]` | Lower | Folded into efficiency |
| Cost | `scored["metrics"]["cost_usd"]` | Lower | Public API pricing × token counts (not invoiced cost) |
| Cites | `scored["citation_grounding"]` | Higher | `grounded/total`; `!N` suffix counts hallucinated lines |

---

## Full scoring pipeline

```
run.sh        scenario session                → results/<tool>/<repo>/transcript.json
                                                 + run_meta.json + claude.log
score.sh      scorer.score_transcript         → scored.json
              (keyword_coverage, citation_grounding,
               efficiency, adoption_score)
judge.sh      judge.call_judge per step       → judged.json
              (llm_quality = scenario_quality)
report.sh     reporter.build_report           → report.md / report.json
              (combines via fairness.compute)
```

`score.sh` and `judge.sh` are independently idempotent — re-running
either is cheap when the transcript hasn't changed. The judge skips
when `judged.json` is newer than `transcript.json` unless `--force`.

---

## Quality gates on the scorer itself (Phase 4 audit)

The scorer can be wrong, and the loop can optimize a wrong scorer.
Three auditors run in parallel after each improvement-loop iteration
and watch the scoring layer:

| Auditor | Asks | Output | Flag if |
|---|---|---|---|
| **Score auditor** ([`audit_scoring.py`](./lib/audit_scoring.py)) | For each fairness check, do you (judge) agree with the hit/miss verdict? | `audit-scoring.<tool>.<repo>.json` | disagreement_rate > 5% |
| **Scenario auditor** ([`audit_scenarios.py`](./lib/audit_scenarios.py)) | What understanding does the LLM show that current checks don't reward? | `audit-scenarios.<repo>.json` | (always — proposes additions/removals as hints to the next iter's reviewer) |
| **Watchdog** ([`audit_watchdog.py`](./lib/audit_watchdog.py)) | Did `llm_quality` move with `keyword_coverage`, or only the metric? | `audit-watchdog.json` | `verdict: suspect` (2 in a row → hard halt) |

All three use the same Opus 4.7 — there's no independent judge model.
A model bias contaminates both layers (accepted v1 limitation).
Per-iteration meta narrative lives in `meta-report.md`.

---

## What's locked, what's tunable

[`locked/locked.yaml`](./locked/locked.yaml) defines what the
improvement loop may *not* touch.

| Locked | Tunable by loop |
|---|---|
| Fairness axes (4 names, structure) | Axis weights (±0.05/iter/axis) |
| Judge model identity (Opus 4.7) | Rubric criterion weights (±0.10/iter/criterion) |
| Judge + auditor prompts | Per-repo TIME_CEILINGS / EFFICIENCY_CEILINGS (±20%/iter) |
| Convergence criteria (4 of them) | Individual checks (add / remove / tighten) |
| Held-out scenarios + gold grades | |
| Orchestration code (`scorer.py`, `fairness.py`, `lib/*.py`, `*.sh`) | |

Any structural change is a versioned event — bump `locked_version`,
re-grade the held-out set.

---

## Held-out validation (the anti-Goodhart anchor)

Three frozen scenarios — `flask-blueprints`, `axum-towers`,
`sense-mcp-flow` — sit in `scenarios/held-out/` with hand-graded
`gold.json` reference scores. Their transcripts and rubrics are
pinned by SHA256 in `locked/held-out.lock`; the loop refuses to
start if any hash drifts.

Each iteration re-judges the 6 frozen transcripts against the
current rubric ([`lib/heldout_rescore.py`](./lib/heldout_rescore.py)),
then computes Spearman correlation between current `llm_quality`
and `gold.json`. **Drop below 0.85 → convergence criterion 4 fails →
loop must stop or be re-anchored.**

This is the bench's anti-Goodhart line: the loop can't tune itself
into a local maximum that disagrees with hand grading.

---

## Convergence criteria (when is the bench done?)

The improvement loop halts cleanly when **all four** hold for **two
consecutive iterations** ([`lib/convergence.py`](./lib/convergence.py)):

1. **Auditor agreement** — score-auditor `disagreement_rate < 5%`.
2. **Rank stability** — per-scenario tool ranks unchanged vs prev iter.
3. **Discrimination** — fairness gap ≥ 0.10 on ≥ ⌈2/3⌉ of scenarios run
   (full bench: ≥4/6).
4. **Held-out correlation** — Spearman vs `gold.json` ≥ 0.85.

Each iter writes per-criterion pass/fail + the distance summary to
`convergence.json` and prints it via `delta.md`. The full
bench-readiness verdict (READY / NOT READY / PANIC / INDETERMINATE)
lives in `results/bench-readiness.md`.

---

## Where to look in the code

| Question | File |
|---|---|
| Combined fairness formula | [`lib/fairness.py`](./lib/fairness.py) |
| Per-repo ceilings + budgets | [`lib/scorer.py`](./lib/scorer.py) (TIME_CEILINGS, EFFICIENCY_CEILINGS, BUDGET_PER_REPO) |
| Check evaluation | [`lib/scorer.py`](./lib/scorer.py) `evaluate_check` |
| `answer_text` vs `audit_text` split | [`lib/scorer.py`](./lib/scorer.py) `read_transcript_texts` |
| Citation extraction + verification | [`lib/grounding.py`](./lib/grounding.py) |
| Judge call + step aggregation | [`lib/judge.py`](./lib/judge.py) |
| Judge prompt | [`lib/judge_prompt.v1.md`](./lib/judge_prompt.v1.md) |
| Report rendering | [`lib/reporter.py`](./lib/reporter.py) |
| Convergence criteria | [`lib/convergence.py`](./lib/convergence.py) |
| Auditors | [`lib/audit_scoring.py`](./lib/audit_scoring.py), [`audit_scenarios.py`](./lib/audit_scenarios.py), [`audit_watchdog.py`](./lib/audit_watchdog.py) |
| Held-out rescorer | [`lib/heldout_rescore.py`](./lib/heldout_rescore.py) |
| What's locked | [`locked/locked.yaml`](./locked/locked.yaml) |

---

## Ship history (in case you wonder how we got here)

| Date | What changed |
|---|---|
| 2026-05-12 | Two-layer model introduced (fairness vs adoption). Original fairness = `0.70·correctness + 0.30·efficiency`. |
| 2026-05-12 | Scoring-engine bug fixes: `answer_text` vs `audit_text` split, true hit-rate `keyword_coverage`, `phrase` check type, glob counts as grep, discoverability ceiling 20. |
| 2026-05-13 | `citation_grounding` block added to `scored.json`, hallucination log, naive ±5 line symbol grep. Not yet folded into fairness. |
| 2026-05-13 | LLM judge (Opus 4.7) + per-scenario rubrics + AI-agent voice prompts. **Fairness reweighted to 10/55/15/20.** Citation grounding folded in. |
| 2026-05-13 | Score auditor + scenario auditor + watchdog wired as Phase 4 of the improvement loop. `meta-report.md` per iter. |
| 2026-05-13 | Convergence criteria, held-out lock, `locked.yaml`, cost ceiling, `readiness.md`, credit-fallback policy. |

Per-commit detail: [`CHANGELOG.md`](./CHANGELOG.md).
