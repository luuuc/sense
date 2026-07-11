# python-django — Sense vertical benchmark

This is the benchmark, the methodology, and the raw data behind the python-django write-ups: how much a structural code index (**Sense**) helps an AI coding agent answer questions about real-world codebases in this stack, measured across several models.

Every scenario is run twice with the same model: a **baseline** arm (the agent's normal tools) and a **sense** arm (the same tools plus the Sense index). Each scenario declares a must-find set of code locations, and the score is **cited recall** — the share of that set the answer pinned to an exact `path:line`. The deltas below are sense minus baseline, so **positive means Sense helped**.

Jump to: [Methodology](#methodology) · [Results](#results) · [Per-model reports](#per-model-reports) · [Per-repo variance](#per-repo-variance)

## Methodology

**The question.** Does giving an AI coding agent a structural index of a codebase make it answer questions about that code more completely and more precisely? Sense is that index: it maps a repo's symbols, call relationships, and dependents so the agent can look them up instead of reading files one at a time.

**The two arms.** Every scenario runs twice with the *same* model and the *same* underlying toolkit. The **baseline** arm uses the agent's normal tools (file reads, grep, and so on). The **sense** arm adds the Sense index on top. Nothing else changes, so any gap between the two is attributable to the index.

**The repositories.** The scenarios run against 6 real-world codebases from this stack, each pinned to a fixed commit so a run is reproducible. They span small libraries to large applications, including ones far too big to fit in a single context window.

**The scenarios.** Each scenario is a realistic, multi-step comprehension task (for example: trace a request from its controller through to persistence and locate the tests that cover it). Each one declares a **must-find set** — the exact code locations a complete, correct answer should surface. Scenarios are written so that a naive text search does not trivially answer them: the relevant code is scattered across non-obvious places.

**The metrics.** The headline is **cited recall**: of the must-find set, the share the answer pinned to an exact `path:line` an agent could jump straight to. Reported alongside it are **mention recall** (named at all, location optional), **relationship correctness** (states the right connection, not just the name), **truthfulness** (no confidently false claims), and **billed tokens** (the context the answer cost to produce). Recall is the goal; tokens are reported but never traded against it.

**Grading.** A separate judge model (Claude Sonnet 4.6) grades each answer's coverage against the authored must-find set, so a confident-sounding but incomplete answer is penalised for what it leaves out. Every `path:line` an answer prints is then checked against the repo at the benchmarked commit; any citation that does not resolve is listed per model in the [citation check](#per-model-reports).

**Repeatability.** Each (model, repo) pair is run more than once and the run-to-run spread is published under [Per-repo variance](#per-repo-variance), so a headline number is trusted only when it is stable rather than a lucky draw.

## Results

The raw numbers, 5 models across 6 repos. Each model's full per-repo tables are linked under [Per-model reports](#per-model-reports).

### Per-model summary

One row per model. **repos** is how many of the vertical's scenarios it was benched on; the two Δ columns are the mean cited-recall lift (sense − baseline) across them — **overall** for the whole scenario, **deps** for the harder `dependents` group (what depends on a given symbol). Positive means Sense helped that model on average.

| model | repos | mean overall Δ | mean deps Δ |
|---|---|---|---|
| claude-opus-4-8 | 6 | +0.05 | +0.16 |
| gpt-5.5 | 6 | +0.08 | +0.17 |
| kimi-for-coding_k2p7 | 6 | +0.06 | +0.18 |
| ollama-cloud_devstral-small-2_24b | 6 | +0.17 | +0.24 |
| ollama-cloud_qwen3-coder-next | 6 | +0.08 | +0.05 |

### Overall cited-recall Δ (sense − baseline), by model × repo

Every cell is the cited-recall lift for one model on one repo. For example, `+0.40` means the sense arm pinned 40 percentage points more of that repo's must-find set to an exact location than the baseline did. A near-zero value is a tie; a `—` means that repo was not benched for that model.

| model | healthchecks | litellm | netbox | saleor | sentry | wagtail |
|---|---|---|---|---|---|---|
| claude-opus-4-8 | +0.00 | +0.00 | +0.11 | +0.15 | +0.03 | +0.00 |
| gpt-5.5 | +0.00 | +0.00 | +0.02 | +0.08 | +0.35 | +0.04 |
| kimi-for-coding_k2p7 | +0.00 | +0.04 | -0.04 | +0.15 | +0.24 | +0.00 |
| ollama-cloud_devstral-small-2_24b | +0.05 | +0.04 | +0.35 | +0.15 | +0.29 | +0.15 |
| ollama-cloud_qwen3-coder-next | +0.00 | +0.13 | +0.13 | +0.04 | +0.00 | +0.19 |

### Efficiency by model (baseline → sense)

What each arm spent to produce its answers, averaged across the model's repos and shown as baseline → sense. These are consumption figures, independent of any provider's price (no dollar cost). **billed** is the tokens you actually pay for (uncached input + output); **cached** is cache-read context; **wall s** is session wall-clock seconds. Lower is cheaper — but recall is never traded for a smaller token bill, so read this alongside the lift above, not instead of it.

| model | wall s | billed tok | cached tok | output tok | billed Δ% |
|---|---|---|---|---|---|
| claude-opus-4-8 | 301 → 265 | 26,275 → 27,135 | 647,674 → 463,662 | 21,885 → 22,240 | +3% |
| gpt-5.5 | 0 → 0 | 139,374 → 126,332 | 560,693 → 528,373 | 13,318 → 13,350 | -9% |
| kimi-for-coding_k2p7 | 0 → 0 | 168,862 → 148,870 | 3,009,131 → 1,798,204 | 34,996 → 36,298 | -12% |
| ollama-cloud_devstral-small-2_24b | 0 → 0 | 680,422 → 816,574 | 0 → 0 | 7,858 → 5,908 | +20% |
| ollama-cloud_qwen3-coder-next | 0 → 0 | 1,351,058 → 1,164,753 | 0 → 0 | 13,576 → 13,720 | -14% |

## Per-model reports

Full per-repo tables and the citation check for each model:

| model | report | citation check |
|---|---|---|
| claude-opus-4-8 | [report.md](claude-opus-4-8/report.md) | [citation-hallucinations.md](claude-opus-4-8/citation-hallucinations.md) |
| gpt-5.5 | [report.md](gpt-5.5/report.md) | [citation-hallucinations.md](gpt-5.5/citation-hallucinations.md) |
| kimi-for-coding_k2p7 | [report.md](kimi-for-coding_k2p7/report.md) | [citation-hallucinations.md](kimi-for-coding_k2p7/citation-hallucinations.md) |
| ollama-cloud_devstral-small-2_24b | [report.md](ollama-cloud_devstral-small-2_24b/report.md) | [citation-hallucinations.md](ollama-cloud_devstral-small-2_24b/citation-hallucinations.md) |
| ollama-cloud_qwen3-coder-next | [report.md](ollama-cloud_qwen3-coder-next/report.md) | [citation-hallucinations.md](ollama-cloud_qwen3-coder-next/citation-hallucinations.md) |

## Per-repo variance

Run-to-run spread per repo (is the headline stable or noise?):

[healthchecks](variance/healthchecks.md) · [litellm](variance/litellm.md) · [netbox](variance/netbox.md) · [saleor](variance/saleor.md) · [sentry](variance/sentry.md) · [wagtail](variance/wagtail.md)
