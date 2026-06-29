# ruby-rails — Sense vertical benchmark

This is the benchmark, the methodology, and the raw data behind the ruby-rails write-ups: how much a structural code index (**Sense**) helps an AI coding agent answer questions about real-world codebases in this stack, measured across several models.

Every scenario is run twice with the same model: a **baseline** arm (the agent's normal tools) and a **sense** arm (the same tools plus the Sense index). Each scenario declares a must-find set of code locations, and the score is **cited recall** — the share of that set the answer pinned to an exact `path:line`. The deltas below are sense minus baseline, so **positive means Sense helped**.

Jump to: [Methodology](#methodology) · [Results](#results) · [Per-model reports](#per-model-reports) · [Per-repo variance](#per-repo-variance)

## Methodology

**The question.** Does giving an AI coding agent a structural index of a codebase make it answer questions about that code more completely and more precisely? Sense is that index: it maps a repo's symbols, call relationships, and dependents so the agent can look them up instead of reading files one at a time.

**The two arms.** Every scenario runs twice with the *same* model and the *same* underlying toolkit. The **baseline** arm uses the agent's normal tools (file reads, grep, and so on). The **sense** arm adds the Sense index on top. Nothing else changes, so any gap between the two is attributable to the index.

**The repositories.** The scenarios run against 13 real-world codebases from this stack, each pinned to a fixed commit so a run is reproducible. They span small libraries to large applications, including ones far too big to fit in a single context window.

**The scenarios.** Each scenario is a realistic, multi-step comprehension task (for example: trace a request from its controller through to persistence and locate the tests that cover it). Each one declares a **must-find set** — the exact code locations a complete, correct answer should surface. Scenarios are written so that a naive text search does not trivially answer them: the relevant code is scattered across non-obvious places.

**The metrics.** The headline is **cited recall**: of the must-find set, the share the answer pinned to an exact `path:line` an agent could jump straight to. Reported alongside it are **mention recall** (named at all, location optional), **relationship correctness** (states the right connection, not just the name), **truthfulness** (no confidently false claims), and **billed tokens** (the context the answer cost to produce). Recall is the goal; tokens are reported but never traded against it.

**Grading.** A separate judge model (Claude Sonnet 4.6) grades each answer's coverage against the authored must-find set, so a confident-sounding but incomplete answer is penalised for what it leaves out. Every `path:line` an answer prints is then checked against the repo at the benchmarked commit; any citation that does not resolve is listed per model in the [citation check](#per-model-reports).

**Repeatability.** Each (model, repo) pair is run more than once and the run-to-run spread is published under [Per-repo variance](#per-repo-variance), so a headline number is trusted only when it is stable rather than a lucky draw.

## Results

The raw numbers, 5 models across 13 repos. Each model's full per-repo tables are linked under [Per-model reports](#per-model-reports).

### Per-model summary

One row per model. **repos** is how many of the vertical's scenarios it was benched on; the two Δ columns are the mean cited-recall lift (sense − baseline) across them — **overall** for the whole scenario, **deps** for the harder `dependents` group (what depends on a given symbol). Positive means Sense helped that model on average.

| model | repos | mean overall Δ | mean deps Δ |
|---|---|---|---|
| claude-opus-4-8 | 13 | +0.26 | +0.48 |
| gpt-5.5 | 13 | +0.13 | +0.29 |
| kimi-for-coding_k2p7 | 13 | +0.14 | +0.18 |
| ollama-cloud_devstral-small-2_24b | 13 | +0.25 | +0.36 |
| ollama-cloud_qwen3-coder-next | 13 | +0.18 | +0.24 |

### Overall cited-recall Δ (sense − baseline), by model × repo

Every cell is the cited-recall lift for one model on one repo. For example, `+0.40` means the sense arm pinned 40 percentage points more of that repo's must-find set to an exact location than the baseline did. A near-zero value is a tie; a `—` means that repo was not benched for that model.

| model | chatwoot | discourse | forem | gitlabhq | langchainrb | llm.rb | lobsters | mastodon | rails | raix | redmine | ruby_llm | solidus |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| claude-opus-4-8 | +0.68 | +0.40 | +0.29 | +0.41 | +0.17 | -0.00 | +0.12 | +0.54 | +0.25 | +0.13 | +0.11 | +0.04 | +0.28 |
| gpt-5.5 | +0.35 | +0.00 | +0.04 | +0.20 | -0.03 | +0.07 | +0.12 | +0.35 | +0.17 | +0.00 | +0.17 | +0.00 | +0.25 |
| kimi-for-coding_k2p7 | +0.35 | +0.10 | +0.02 | +0.20 | +0.12 | +0.26 | +0.12 | +0.09 | +0.08 | +0.17 | +0.00 | +0.11 | +0.20 |
| ollama-cloud_devstral-small-2_24b | +0.74 | +0.35 | +0.25 | +0.24 | +0.14 | +0.07 | +0.09 | +0.43 | +0.25 | -0.20 | +0.58 | +0.07 | +0.27 |
| ollama-cloud_qwen3-coder-next | +0.29 | +0.10 | +0.27 | +0.24 | +0.29 | +0.20 | +0.12 | +0.07 | +0.31 | +0.03 | -0.03 | +0.04 | +0.35 |

### Efficiency by model (baseline → sense)

What each arm spent to produce its answers, averaged across the model's repos and shown as baseline → sense. These are consumption figures, independent of any provider's price (no dollar cost). **billed** is the tokens you actually pay for (uncached input + output); **cached** is cache-read context; **wall s** is session wall-clock seconds. Lower is cheaper — but recall is never traded for a smaller token bill, so read this alongside the lift above, not instead of it.

| model | wall s | billed tok | cached tok | output tok | billed Δ% |
|---|---|---|---|---|---|
| claude-opus-4-8 | 267 → 245 | 23,198 → 23,203 | 834,951 → 642,861 | 20,581 → 19,801 | +0% |
| gpt-5.5 | 0 → 0 | 152,879 → 139,693 | 627,929 → 931,151 | 10,944 → 11,788 | -9% |
| kimi-for-coding_k2p7 | 0 → 0 | 139,539 → 125,416 | 2,436,620 → 2,505,165 | 19,376 → 19,016 | -10% |
| ollama-cloud_devstral-small-2_24b | 0 → 0 | 2,044,783 → 1,595,901 | 0 → 0 | 6,602 → 4,995 | -22% |
| ollama-cloud_qwen3-coder-next | 0 → 0 | 1,067,682 → 1,215,844 | 0 → 0 | 10,014 → 10,334 | +14% |

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

[chatwoot](variance/chatwoot.md) · [discourse](variance/discourse.md) · [forem](variance/forem.md) · [gitlabhq](variance/gitlabhq.md) · [langchainrb](variance/langchainrb.md) · [llm.rb](variance/llm.rb.md) · [lobsters](variance/lobsters.md) · [mastodon](variance/mastodon.md) · [rails](variance/rails.md) · [raix](variance/raix.md) · [redmine](variance/redmine.md) · [ruby_llm](variance/ruby_llm.md) · [solidus](variance/solidus.md)
