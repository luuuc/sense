# ruby-rails — cross-model matrix

Sense vs baseline cited-recall by model. `overall Δ` is the whole-scenario cited-recall lift; `deps Δ` is the discriminator `dependents` group (the headline where a scenario has one). Each model is benched independently under `results/vertical/ruby-rails/<model>/`.

## Per-model summary

| model | repos | mean overall Δ | mean deps Δ |
|---|---|---|---|
| claude-opus-4-8 | 13 | +0.25 | +0.52 |
| gpt-5.5 | 5 | +0.17 | +0.28 |

## Overall cited-recall Δ (sense − baseline), by model × repo

| model | chatwoot | discourse | forem | gitlabhq | langchainrb | llm.rb | lobsters | mastodon | rails | raix | redmine | ruby_llm | solidus |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| claude-opus-4-8 | +0.44 | +0.42 | +0.27 | +0.48 | +0.09 | +0.09 | +0.00 | +0.52 | +0.19 | +0.00 | +0.13 | +0.22 | +0.38 |
| gpt-5.5 | +0.29 | +0.29 | +0.12 | — | — | — | — | +0.09 | — | — | — | — | +0.08 |

## Efficiency by model (baseline → sense, means across the model's repos)

Session time and token consumption; price-independent (no cost). billed = uncached input + output; cached = cache-read; both arms shown as base → sense.

| model | wall s | billed tok | cached tok | output tok | billed Δ% |
|---|---|---|---|---|---|
| claude-opus-4-8 | 252 → 226 | 21,586 → 21,202 | 820,710 → 631,293 | 19,378 → 18,466 | -2% |
| gpt-5.5 | 0 → 0 | 191,583 → 169,290 | 896,269 → 715,699 | 12,727 → 12,191 | -12% |
