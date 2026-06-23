# ruby-rails — cross-model matrix

Sense vs baseline cited-recall by model. `overall Δ` is the whole-scenario cited-recall lift; `deps Δ` is the discriminator `dependents` group (the headline where a scenario has one). Each model is benched independently under `results/vertical/ruby-rails/<model>/`.

## Per-model summary

| model | repos | mean overall Δ | mean deps Δ |
|---|---|---|---|
| claude-opus-4-8 | 13 | +0.26 | +0.48 |
| kimi-for-coding_k2p7 | 2 | +0.26 | +0.25 |
| ollama-cloud_qwen3-coder-next | 13 | +0.19 | +0.25 |

## Overall cited-recall Δ (sense − baseline), by model × repo

| model | chatwoot | discourse | forem | gitlabhq | langchainrb | llm.rb | lobsters | mastodon | rails | raix | redmine | ruby_llm | solidus |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| claude-opus-4-8 | +0.68 | +0.40 | +0.29 | +0.41 | +0.17 | -0.00 | +0.12 | +0.54 | +0.25 | +0.13 | +0.11 | +0.04 | +0.28 |
| kimi-for-coding_k2p7 | — | — | — | — | — | — | — | — | +0.08 | +0.43 | — | — | — |
| ollama-cloud_qwen3-coder-next | +0.15 | +0.10 | +0.73 | +0.24 | +0.29 | +0.20 | +0.12 | -0.09 | +0.31 | +0.03 | -0.03 | +0.04 | +0.35 |

## Efficiency by model (baseline → sense, means across the model's repos)

Session time and token consumption; price-independent (no cost). billed = uncached input + output; cached = cache-read; both arms shown as base → sense.

| model | wall s | billed tok | cached tok | output tok | billed Δ% |
|---|---|---|---|---|---|
| claude-opus-4-8 | 267 → 245 | 23,198 → 23,203 | 834,951 → 642,861 | 20,581 → 19,801 | +0% |
| kimi-for-coding_k2p7 | 0 → 0 | 93,510 → 100,861 | 1,096,405 → 2,807,936 | 13,196 → 16,356 | +8% |
| ollama-cloud_qwen3-coder-next | 0 → 0 | 1,139,042 → 1,339,319 | 0 → 0 | 9,670 → 10,337 | +18% |
