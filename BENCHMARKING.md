# How Change Is Piloted

Sense does not ship behavioral change on taste, intuition, or a plausible argument. A benchmark proves it first. This page is the bar your contribution is held to, so you know it before you open a PR. The raw harness and data live in [`bench/`](bench/).

This is the public, distilled version of how we measure. It exists so a contributor understands the standard. It is not the full internal playbook.

---

## The principle

Every change that affects what the AI sees (a new language, a framework's idioms, a resolver fix, a tuning of output) earns the right to ship by moving a benchmark in the right direction, on real codebases, without regressing the rest. If it can't be measured, it can't be argued.

The benchmark is the pilot. Product fixes are usually a byproduct of benching a real scenario, not the starting point. We find the gap by measuring, then fix it.

---

## The rules we hold ourselves to

These are the same rules the internal methodology runs on. They are publishable on purpose, because a benchmark that only flatters the tool measures nothing.

1. **The agent is the constant; Sense is a tool it reaches for.** Both arms are the same capable agent on the same frontier model — one working with Sense, one with only grep, read, and its own reasoning (the baseline). The question is never "is Sense useful in the abstract," it is "where does the tool let the agent reach structure it could not assemble by reading and grepping alone." That reach is hardest to see from the inside, because a strong agent reads and recalls its way to most answers and feels done, which is exactly the false confidence the bench is built to catch.

2. **A proxy is never a verdict.** grep-reachability is not "the baseline would have found it." A single run is noise. Metadata is not evidence. Conclusions come from reading the full transcripts of both arms, side by side, per target.

3. **Truth is the transcripts and objective recall, not the LLM judge.** A reference-blind judge is blind to omission and will rate a half-complete answer "exhaustive." When the judge contradicts the transcripts, we fix the judge. The judge is made reference-aware: it grades against the must-find set and each item's expected relationship. See [the judging contract in `bench/`](bench/).

4. **Measure on the frontier model, more than once.** A win on a weaker model is not a win, it inflates the tool. Single runs are noise. Results are taken across repeated runs on the strongest available model, with the judge pinned.

5. **The headline is reach at parity, not cost.** The signal is how much of the relevant structure the agent actually reaches, measured at equal billed tokens, plus the dependents only the Sense arm finds. Token and time savings are real and reported, but they are a side effect, not the claim. See [NON-GOALS.md](NON-GOALS.md).

6. **Honesty guardrails.** The two arms differ only by toolset, never by prompt. The scenario is a real task a maintainer would recognize, never contrived, never leaking the answer. Where the agent does as well or better without Sense, it is reported. Pinned commits, published harness, traceable numbers.

---

## What this means for your contribution

- **A new language or framework** is proven by scanning real repos in that ecosystem and showing the graph, blast, and conventions outputs are correct and that dead-code does not falsely flag framework-reachable symbols. Correctness on real code, not a fixture that happens to pass.

- **A resolver or output change** is proven by a before/after on the benchmark: it must move reach or correctness in the right direction without regressing other repos.

- **A new feature** is not in scope (see [NON-GOALS.md](NON-GOALS.md)), so there is nothing to bench. Open an issue first.

If you are unsure how to bench your change, open an issue and describe the scenario. The harness in [`bench/`](bench/) is the starting point, and a real task on a real repo is always the right shape.
