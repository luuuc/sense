# Rails-vertical scoring — Sense vs baseline

_Judge: claude-sonnet-4-6 (via subscription CLI). All axes averaged across runs. Regenerated FROM DISK by `bench/lib/scoreboard.py` (reproducible).

## Headline
**11 wins / 2 ties / 0 losses** across 13 repos on `cited_recall`. Mean cited Δ (sense−baseline): **+0.248**.

Anti-fabrication wins (⚑ — baseline asserts a wrong relation, Sense does not): **gitlabhq, discourse, solidus, forem**.

| repo | cited b→s (Δ) | deps-delta | B-score b→s | related b→s | grnd b/s | contra b/s | verdict |
|------|---------------|-----------|-------------|-------------|:--------:|:---------:|:-------:|
| mastodon | 0.29→0.81 (+0.52) | +0.71 | 0.50→0.88 | 0.57→0.95 | 1.00/1.00 | 0/0 | **WIN** |
| gitlabhq | 0.23→0.71 (+0.48) | +0.62 | 0.43→0.72 | 0.42→0.52 | 0.97/1.00 | 1/0 | **WIN ⚑** |
| chatwoot | 0.44→0.88 (+0.44) | +0.64 | 0.55→0.90 | 0.41→0.88 | 1.00/0.97 | 0/1 | **WIN** |
| discourse | 0.43→0.85 (+0.42) | +0.60 | 0.62→0.89 | 0.76→0.89 | 0.98/1.00 | 1/0 | **WIN ⚑** |
| solidus | 0.38→0.77 (+0.38) | +0.52 | 0.55→0.87 | 0.58→1.00 | 0.98/1.00 | 1/0 | **WIN ⚑** |
| forem | 0.29→0.56 (+0.27) | +0.38 | 0.52→0.72 | 0.67→0.85 | 0.98/1.00 | 1/0 | **WIN ⚑** |
| ruby_llm 🔸 | 0.61→0.83 (+0.22) | — | 0.77→0.87 | 0.93→0.85 | 1.00/1.00 | 0/0 | **WIN** |
| redmine | 0.67→0.80 (+0.13) | +0.13 | 0.80→0.85 | 0.94→0.85 | 1.00/1.00 | 0/0 | **WIN** |
| rails | 0.76→0.94 (+0.19) | +0.56 | 0.82→0.96 | 0.81→0.96 | 1.00/1.00 | 0/0 | **WIN** |
| llm.rb 🔸 | 0.26→0.35 (+0.09) | — | 0.57→0.62 | 0.91→0.89 | 1.00/1.00 | 0/0 | **WIN** |
| langchainrb 🔸 | 0.24→0.33 (+0.09) | — | 0.58→0.63 | 0.98→0.98 | 1.00/1.00 | 0/0 | **WIN** |
| lobsters | 1.00→1.00 (+0.00) | — | 0.79→0.79 | 0.14→0.14 | 1.00/1.00 | 0/0 | **TIE** |
| raix 🔸 | 0.70→0.70 (+0.00) | — | 0.81→0.81 | 0.90→0.90 | 1.00/1.00 | 0/0 | **TIE** |

🔸 gem / small library. ⚑ anti-fabrication win. deps-delta = cited_recall on the scattered-dependents discriminator group.

## Where Sense wins, and why
- **Scattered non-obvious fan-out (the big wins):** mastodon +0.52, gitlabhq +0.48, chatwoot +0.44, discourse +0.42, solidus +0.38 — central models whose heterogeneous dependents grep/ls cannot assemble; one `sense_blast <Model>` returns the resolved set.
- **Anti-fabrication (⚑ gitlabhq, discourse, solidus, forem):** the baseline confidently mis-describes a dependent's role; Sense states the verified one (graded vs the gold `relation` by the Sonnet reference-aware judge).
- **Location-pinning:** `cited_recall` (path:line) separates further than mention; Sense pins where the baseline names vaguely.
- **The framework case (rails):** the win is isolated to the non-memorized query-compilation internals; both arms tie on the public API the model recites from training.

## Honest read
- **Gems are cited wins, not relation wins.** ruby_llm (+0.22), llm.rb / langchainrb (+0.09) win on `cited_recall` but the `related` axis is flat-to-negative — the baseline describes small-library code competently. The honest small-repo boundary.
- **redmine (the 11th win)** is the medium-repo case: a cited win (+0.13 on the clean ×3, sense stable 11/13 vs baseline 8/8/12) with a relation tie — the gem pattern at medium scale. Modest.
- **chatwoot** carries one sense-side contradiction under Sonnet (grounded 0.97); the win is completeness, not anti-fabrication.
- **Two true ties:** lobsters (1.00/1.00 — small/readable, both find everything) and raix (0.70/0.70 — tiny colocated mixin gem).

## Provenance (why this is now reproducible)
- **Judge: Claude Sonnet 4.6**, reference-aware, via the subscription CLI. **All 66 runs** (13 repos × baseline/sense × their runs) re-judged 2026-06-20 — the board was previously judged by `claude-opus-4-7` while mislabeled "Sonnet", and the numbers did not trace to disk.
- **Regenerate this file from disk:** `python3 bench/lib/scoreboard.py` (reads `<arm>/<repo>/run-*/{scored,judged}.json`, averages across runs). `cited_recall` is objective (the scorer); `related`/`grounded`/`contra` are the Sonnet judge.
