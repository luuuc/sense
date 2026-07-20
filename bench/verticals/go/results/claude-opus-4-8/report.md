# claude-opus-4-8 — Sense vertical benchmark

This is the benchmark, the methodology, and the raw data behind the claude-opus-4-8 write-ups: how much a structural code index (**Sense**) helps an AI coding agent answer questions about real-world codebases in this stack, measured across several models.

Every scenario is run twice with the same model: a **baseline** arm (the agent's normal tools) and a **sense** arm (the same tools plus the Sense index). Each scenario declares a must-find set of code locations, and the score is **cited recall** — the share of that set the answer pinned to an exact `path:line`. The deltas below are sense minus baseline, so **positive means Sense helped**.

Jump to: [Methodology](#methodology) · [Results](#results) · [Per-model reports](#per-model-reports) · [Per-repo variance](#per-repo-variance)

## Methodology

**The question.** Does giving an AI coding agent a structural index of a codebase make it answer questions about that code more completely and more precisely? Sense is that index: it maps a repo's symbols, call relationships, and dependents so the agent can look them up instead of reading files one at a time.

**The two arms.** Every scenario runs twice with the *same* model and the *same* underlying toolkit. The **baseline** arm uses the agent's normal tools (file reads, grep, and so on). The **sense** arm adds the Sense index on top. Nothing else changes, so any gap between the two is attributable to the index.

**The repositories.** The scenarios run against 8 real-world codebases from this stack, each pinned to a fixed commit so a run is reproducible. They span small libraries to large applications, including ones far too big to fit in a single context window.

**The scenarios.** Each scenario is a realistic, multi-step comprehension task (for example: trace a request from its controller through to persistence and locate the tests that cover it). Each one declares a **must-find set** — the exact code locations a complete, correct answer should surface. Scenarios are written so that a naive text search does not trivially answer them: the relevant code is scattered across non-obvious places.

**The metrics.** The headline is **cited recall**: of the must-find set, the share the answer pinned to an exact `path:line` an agent could jump straight to. Reported alongside it are **mention recall** (named at all, location optional), **relationship correctness** (states the right connection, not just the name), **truthfulness** (no confidently false claims), and **billed tokens** (the context the answer cost to produce). Recall is the goal; tokens are reported but never traded against it.

**Grading.** A separate judge model (Claude Sonnet 4.6) grades each answer's coverage against the authored must-find set, so a confident-sounding but incomplete answer is penalised for what it leaves out. Every `path:line` an answer prints is then checked against the repo at the benchmarked commit; any citation that does not resolve is listed per model in the [citation check](#per-model-reports).

**Repeatability.** Each (model, repo) pair is run more than once and the run-to-run spread is published under [Per-repo variance](#per-repo-variance), so a headline number is trusted only when it is stable rather than a lucky draw.

## Results

The raw numbers, 2 models across 8 repos. Each model's full per-repo tables are linked under [Per-model reports](#per-model-reports).

### Per-model summary

One row per model. **repos** is how many of the vertical's scenarios it was benched on; the two Δ columns are the mean cited-recall lift (sense − baseline) across them — **overall** for the whole scenario, **deps** for the harder `dependents` group (what depends on a given symbol). Positive means Sense helped that model on average.

| model | repos | mean overall Δ | mean deps Δ |
|---|---|---|---|
| dryruns-20260719 | 4 | +0.13 | +0.50 |
| dryruns-20260720 | 4 | +0.39 | — |

### Overall cited-recall Δ (sense − baseline), by model × repo

Every cell is the cited-recall lift for one model on one repo. For example, `+0.40` means the sense arm pinned 40 percentage points more of that repo's must-find set to an exact location than the baseline did. A near-zero value is a tie; a `—` means that repo was not benched for that model.

| model | consul-full | consul-probe | dolt | grpc-go | nomad-full | nomad-probe | pebble | teleport |
|---|---|---|---|---|---|---|---|---|
| dryruns-20260719 | — | — | +0.29 | -0.19 | — | — | +0.43 | +0.00 |
| dryruns-20260720 | +0.31 | +0.38 | — | — | +0.33 | +0.53 | — | — |

### Efficiency by model (baseline → sense)

What each arm spent to produce its answers, averaged across the model's repos and shown as baseline → sense. These are consumption figures, independent of any provider's price (no dollar cost). **billed** is the tokens you actually pay for (uncached input + output); **cached** is cache-read context; **wall s** is session wall-clock seconds. Lower is cheaper — but recall is never traded for a smaller token bill, so read this alongside the lift above, not instead of it.

| model | wall s | billed tok | cached tok | output tok | billed Δ% |
|---|---|---|---|---|---|
| dryruns-20260719 | 616 → 472 | 46,323 → 37,504 | 3,552,174 → 2,293,860 | 45,232 → 36,037 | -19% |
| dryruns-20260720 | 561 → 484 | 44,372 → 38,829 | 2,465,003 → 2,496,803 | 43,028 → 37,687 | -12% |

## Per-model reports

Full per-repo tables and the citation check for each model:

| model | report | citation check |
|---|---|---|
| dryruns-20260719 | — | — |
| dryruns-20260720 | — | — |
