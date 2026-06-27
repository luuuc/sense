# results — cross-model matrix

Sense vs baseline cited-recall by model. `overall Δ` is the whole-scenario cited-recall lift; `deps Δ` is the discriminator `dependents` group (the headline where a scenario has one). Each model is benched independently under `verticals/results/results/<model>/`.

## Per-model summary

| model | repos | mean overall Δ | mean deps Δ |
|---|---|---|---|
| claude-opus-4-8 | 13 | +0.26 | +0.48 |
| gpt-5.5 | 13 | +0.13 | +0.29 |
| kimi-for-coding_k2p7 | 12 | +0.13 | +0.16 |
| ollama-cloud_devstral-small-2_24b | 10 | +0.24 | +0.41 |
| ollama-cloud_qwen3-coder-next | 13 | +0.18 | +0.24 |

## Overall cited-recall Δ (sense − baseline), by model × repo

| model | chatwoot | discourse | forem | gitlabhq | langchainrb | llm.rb | lobsters | mastodon | rails | raix | redmine | ruby_llm | solidus |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| claude-opus-4-8 | +0.68 | +0.40 | +0.29 | +0.41 | +0.17 | -0.00 | +0.12 | +0.54 | +0.25 | +0.13 | +0.11 | +0.04 | +0.28 |
| gpt-5.5 | +0.35 | +0.00 | +0.04 | +0.20 | -0.03 | +0.07 | +0.12 | +0.35 | +0.17 | +0.00 | +0.17 | +0.00 | +0.25 |
| kimi-for-coding_k2p7 | +0.35 | +0.10 | +0.02 | — | +0.12 | +0.26 | +0.12 | +0.09 | +0.08 | +0.17 | +0.00 | +0.11 | +0.20 |
| ollama-cloud_devstral-small-2_24b | +0.74 | — | +0.25 | — | +0.14 | +0.07 | +0.09 | +0.43 | — | -0.20 | +0.58 | +0.07 | +0.27 |
| ollama-cloud_qwen3-coder-next | +0.29 | +0.10 | +0.27 | +0.24 | +0.29 | +0.20 | +0.12 | +0.07 | +0.31 | +0.03 | -0.03 | +0.04 | +0.35 |

## Efficiency by model (baseline → sense, means across the model's repos)

Session time and token consumption; price-independent (no cost). billed = uncached input + output; cached = cache-read; both arms shown as base → sense.

| model | wall s | billed tok | cached tok | output tok | billed Δ% |
|---|---|---|---|---|---|
| claude-opus-4-8 | 267 → 245 | 23,198 → 23,203 | 834,951 → 642,861 | 20,581 → 19,801 | +0% |
| gpt-5.5 | 0 → 0 | 152,879 → 139,693 | 627,929 → 931,151 | 10,944 → 11,788 | -9% |
| kimi-for-coding_k2p7 | 0 → 0 | 138,280 → 123,842 | 2,444,066 → 2,464,073 | 18,484 → 18,683 | -10% |
| ollama-cloud_devstral-small-2_24b | 0 → 0 | 1,202,933 → 1,626,523 | 0 → 0 | 6,164 → 4,938 | +35% |
| ollama-cloud_qwen3-coder-next | 0 → 0 | 1,067,682 → 1,215,844 | 0 → 0 | 10,014 → 10,334 | +14% |
