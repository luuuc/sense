# bench improvement loop — End Goal

*Anchor doc. Read this before changing a phase script, a convergence criterion, a tunable bound, or a stop condition. Loop changes that can't be defended against these goals don't ship.*

The bench's end goal lives at `bench/end-goal.md`. This document is about the *machine that gets the bench there*.

---

## North star

**The loop's job is to make the bench rock-solid — and to know when it's done.**

"Rock-solid" is not "the loop ran N iterations". It is the five-part convergence test in `bench/end-goal.md` holding for two consecutive iterations. The loop's purpose is to reach that state cleanly, prove it, and stop.

---

## What the loop optimizes for

**Convergence.** Specifically, the bench passes all four criteria for two iterations in a row:

1. Score-auditor disagreement rate <5% across 2 consecutive iterations.
2. Per-scenario tool ranks stable across the 2 iterations (no flips).
3. Tool discrimination ≥0.10 fairness gap on ≥4 of 6 scenarios.
4. Held-out validation correlation ≥0.85 with hand-graded reference scores.

A change to a tunable is good iff it moves the loop measurably closer to all four holding simultaneously. A change that improves one criterion at the cost of another is suspect and needs the watchdog's explicit pass.

---

## What the loop does NOT optimize for

- **Not maximizing Sense's score.** The loop measures the bench, not the tool.
- **Not maximizing the Sense-vs-baseline gap.** A wider gap that doesn't track transcript quality is Goodhart's law.
- **Not minimizing iteration count.** A loop that "converges" in two iterations by accepting a low bar has failed.
- **Not minimizing token spend.** Cost discipline is a *guardrail* (per-loop ceiling + per-session timeout), not an objective.
- **Not improving its own orchestration.** The loop tunes the bench, never itself.

---

## Tunable vs locked

The loop reads `bench/locked/locked.yaml` at start and refuses any improvement that targets a locked entry.

**Tunable within bounds:**

- `TIME_CEILINGS`, `EFFICIENCY_CEILINGS` — ±20% steps per iteration, audit-justified.
- Fairness formula axis weights — ±0.05 per axis per iteration.
- Judge rubric weights within a scenario — ±0.10 per criterion per iteration.
- Add/remove/edit individual checks via scenario-auditor proposals, gated by Phase 3 rollback.

**Locked, always:**

- Orchestration code (the loop itself).
- The held-out validation set — scenarios, rubrics, transcripts, gold grades.
- Judge model identity (Opus 4.7).
- Fairness-formula structure (axes can be reweighted, not added/removed).
- Convergence criteria.
- Judge prompt and all auditor prompts. *(The loop must not lobotomize its own auditor.)*

A change outside the tunable set is not a loop-iteration change. It is a versioned bench change, made by a human, requiring a held-out re-grade.

---

## Stop conditions

The loop halts cleanly on any one of:

- All four convergence criteria hold for 2 consecutive iterations. *(Success.)*
- Cost ceiling hit (`--max-cost-usd`, default $10 — lowered from the pitch's $15 after Card 15 e2e revealed iter-1 actually costs ~$13).
- Max iterations reached (default 10).
- Watchdog flags ≥2 consecutive iterations as suspect.
- Held-out lockfile mismatch. *(Panic — refuses to continue.)*
- Human SIGINT.

On any halt: emit `results/bench-readiness.md` — one page, plain language, citing the convergence block as evidence, deciding "ready" or "not yet — here's what's holding it back".

The readiness report is the loop's *real* deliverable. Iteration count, score deltas, cost spent — all secondary. The question the report answers is the question the bench exists to answer: *is this benchmark ready to score code-intelligence tools fairly?*

---

## The credit-and-cost discipline

Cost is a *comparability* metric, not an accounting one. A token is a token. Costs are computed from public API pricing regardless of how the call was actually billed (API key vs OAuth subscription fallback). Recording subscription-mode discounts would make iter-N look artificially cheap and break the convergence cost-trend.

This is a non-negotiable: if you find yourself wanting to add `billing_mode` to a cost record, you have lost the plot. Stop. Re-read this section.

---

## The anti-drift contract

The loop's job is to converge *the bench* on a fair, reproducible measurement. The risks against that:

- **Overfitting to the six tunable repos** — caught by the sense-mcp-flow held-out scenario.
- **Overfitting to scenario shape** — caught by flask-blueprints.
- **Overfitting to known symbol sets** — caught by axum-towers.
- **Goodharting the judge** — the judge and auditor prompts are locked; the loop can only tune *within* the rubric, never the rubric itself.
- **Goodharting the watchdog** — same; the watchdog prompt is locked.
- **Loop modifies the held-out set** — `held-out.lock` SHA256-pins every held-out file; the loop refuses to continue on any mismatch.

If any of these fire, the loop halts. It does not "work around" them.

---

## The per-iteration check

Every iteration ends with `delta.md` and a "Distance from convergence" block printed to stdout. The block names which of the four criteria pass, which fail, and by how much. A human reading on the train can answer:

- Did this iteration help?
- Was it the bench or the tools that moved?
- How close are we to done?
- Should I stop the loop?

If you can't answer those four questions from the delta report, the delta report is broken and gets fixed before anything else.

---

## The "is the loop still serving the goal?" test

Before any change to the improvement loop, ask:

- Does this change move the bench closer to converged, or just closer to "iter complete"?
- Does this change protect against a real drift mode, or against a hypothetical one?
- Does this change preserve the cost-as-comparability rule, or does it sneak in accounting?
- Does this change keep "loop tunes bench, never itself" intact?

If the answer is "iter complete", "hypothetical", "sneaks in accounting", or "lets the loop touch itself" — drop the change.
