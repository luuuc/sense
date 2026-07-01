# bench — End Goal

*Anchor doc. Read this before changing a scenario, a rubric, a score formula, or a ceiling. If a change can't be defended against these goals, it doesn't ship.*

---

## North star

**A benchmark that scores code-intelligence tools for AI agents, not humans.**

The benchmark exists to answer one question, repeatedly and trustworthily: *given a realistic code task, does this tool let an AI agent finish with small targeted lookups, or does it force a fresh exploration of the codebase?* Every scenario, every rubric criterion, every score weight serves that question.

---

## What "for AI agents, not humans" means concretely

- **Audience is the next agent.** Scenario prompts say so explicitly ("hand the next agent…"). Rubric criteria score for "could the agent edit from this?" — not "did the human reader feel informed?".
- **No prose-quality bonuses.** A beautifully-written answer that lacks file:line citations scores worse than a terse list of file:line + reasons. The LLM judge is steered toward that prior.
- **Citations and call-orders are first-class.** "file:line for every method in the chain, in order" is the bar.
- **The output of the bench is differential.** The same agent with the tool (sense) vs without it (baseline), with a single fairness number per scenario. Absolute scores matter less than the gap the tool opens on the same task.

---

## What the bench claims when it says it's ready

1. **Scores are reproducible.** Same transcripts in → same scores out, modulo judge variance within published bounds.
2. **The judge agrees with itself.** Score-auditor disagreement <5% across two consecutive iterations.
3. **Ranks are stable.** Per-scenario tool ranks do not flip across two iterations.
4. **It discriminates.** ≥0.10 fairness gap on at least 4 of 6 scenarios — the bench actually distinguishes code-intel from baseline.
5. **It tracks human judgment.** Held-out validation correlation ≥0.85 with hand-graded reference scores.

These five conditions are not aspirational — they are the convergence criteria the improvement loop tests against. If any one fails, the bench is not ready.

---

## What the bench explicitly does NOT claim

- **Not a benchmark of AI model quality.** The model is fixed (Opus 4.7); the bench measures tool-conditioned answer quality.
- **Not a real-world end-to-end task benchmark.** Each scenario is a bounded code-comprehension question with a frozen rubric, not a multi-PR engineering deliverable.
- **Not a cost benchmark.** Cost is computed from public API pricing for comparability across runs, not for any claim about which tool is cheaper in production.
- **Not a human-readability benchmark.** A high-scoring answer is not necessarily a good human-facing explanation.
- **Not a measure of MCP-tool fluency in isolation.** The adoption layer is a secondary signal used for code-intel-vs-code-intel comparisons; it never feeds into fairness.

---

## The six tunable scenarios + three held-out

**Tunable (`bench/scenarios/*.yaml`):** flask, gin, axum, discourse, javalin, nextjs. These are what the improvement loop adjusts. Scenarios, rubric weights within bounds, time ceilings — all in scope.

**Held-out (`bench/scenarios/held-out/`):** flask-blueprints, axum-towers, sense-mcp-flow. Frozen. The loop never edits these and never re-runs the tools against them. Transcripts are committed; only re-scoring happens per iteration. Held-out exists to detect drift modes the tunable set cannot — scenario-shape overfit (flask-blueprints), symbol-set overfit (axum-towers), repo-set overfit (sense-mcp-flow, the dogfood case).

---

## The anti-drift contract

The bench cannot move faster than its anchor. Two anchors:

1. **Held-out hand grades.** Re-grade the held-out set by hand every 5 iterations or any time rubric weights move beyond ±0.10 cumulative. If the loop drifts away from human judgment, the held-out correlation drops first.
2. **`bench/global/locked/locked.yaml`.** Lists what the loop may never touch: the four fairness-formula axes, the judge model, the judge/auditor prompt paths, the convergence criteria. The loop reads this file at start and refuses any improvement that targets a locked entry.

A change to `locked.yaml` is a versioned bench change — bump the version, re-grade the held-out set, do not pretend the previous bench's scores still mean the same thing.

---

## The "is it still serving the goal?" test

Before any change to bench, ask:

- Does this change improve discrimination between tools, or does it just shuffle absolute scores?
- Does this change make the bench better at scoring for the next-agent audience, or better at scoring for a human reader?
- Does this change strengthen or weaken the held-out correlation? (If you can't tell, you haven't thought hard enough.)
- Could a tool author win on this change by improving the *real* capability, or only by gaming the metric?

If the answer is "shuffle absolute scores", "for a human reader", "weakens correlation", or "game the metric" — drop the change.
