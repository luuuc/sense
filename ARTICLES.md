# Articles

Writing about Sense: head-to-head benchmarks, codebase-intelligence deep-dives, and the reasoning behind the tool.

For the numbers themselves, see the live leaderboard at [benchmark.html](https://luuuc.github.io/sense/benchmark.html) and the raw harness in [`bench/`](bench/).

## Published

In chronological order.

- 2026-04-24 — [Context fragmentation is the real bottleneck in AI coding](https://medium.com/@lucdiallo/context-fragmentation-is-the-real-bottleneck-in-ai-coding-5c2f0ab5848e) (Medium)
- 2026-05-01 — [Codebase intelligence in the age of AI: a map of the space](https://medium.com/@lucdiallo/codebase-intelligence-in-the-age-of-ai-a-map-of-the-space-5fa7d349887d) (Medium)
- 2026-05-06 — [Hybrid code search: building the engine your AI tool needs](https://medium.com/@lucdiallo/hybrid-code-search-building-the-engine-your-ai-tool-needs-7decff74f678) (Medium)
- 2026-05-08 — [What codebase intelligence actually does (and where it doesn't)](https://medium.com/@lucdiallo/what-codebase-intelligence-actually-does-and-where-it-doesnt-0efafeae2d49) (Medium)
- 2026-05-15 — How do you benchmark an MCP server you built? ([Medium](https://medium.com/@lucdiallo/how-do-you-benchmark-an-mcp-server-you-built-b8eb70bf49f3), [dev.to](https://dev.to/luuuc/how-do-you-benchmark-an-mcp-server-you-built-2e8j))

## Benchmarking AI on Ruby and Rails

How well does an AI agent navigate real Ruby and Rails code, with and without a structural map? This series runs the same maintainer task on each repo twice, plain Claude Code as the baseline and Claude Code plus Sense, on an open and reproducible harness, then reports where Sense earns its place and where it does not. It spans 13 codebases, from large Rails apps down to the Ruby AI-stack gems, with 6 companion engineering pieces on dev.to, a series synthesis, and a finale on both platforms.

- 2026-06-29 — [Chatwoot is one of the best-built Rails apps shipping. That's exactly why my AI agent failed on it.](https://medium.com/@lucdiallo/chatwoot-is-one-of-the-best-built-rails-apps-shipping-thats-exactly-why-my-ai-agent-failed-on-it-034044346de2) (Medium)
- 2026-06-30 — [Mastodon found a flaw in my own tool. Fixing it is the best part of this story.](https://medium.com/@lucdiallo/mastodon-found-a-flaw-in-my-own-tool-fixing-it-is-the-best-part-of-this-story-a27a127fa33a) (Medium)
- 2026-06-30 — [The AI judge that called a half-finished audit 'exhaustive'](https://dev.to/luuuc/the-ai-judge-that-called-a-half-finished-audit-exhaustive-45n7) (dev.to)
- 2026-07-01 — [GitLab is the biggest Rails monolith there is. How much of it can an AI map?](https://medium.com/@lucdiallo/gitlab-is-the-biggest-rails-monolith-there-is-how-much-of-it-can-an-ai-map-39334bc31308) (Medium)
- 2026-07-02 — [Audit your AI agent's blind spots in 4 commands](https://dev.to/luuuc/audit-your-ai-agents-blind-spots-in-4-commands-51nb) (dev.to)
- 2026-07-02 — [On Discourse, watch an AI go from confident to correct.](https://medium.com/@lucdiallo/on-discourse-watch-an-ai-go-from-confident-to-correct-fc7764d238ff) (Medium)
- 2026-07-03 — [Your AI has read every post on dev.to. Does it know how dev.to is built?](https://medium.com/@lucdiallo/your-ai-has-read-every-post-on-dev-to-does-it-know-how-dev-to-is-built-d65406d88f3d) (Medium)
- 2026-07-04 — [I recorded my agent auditing a 36k-file Rails app: the play-by-play](https://dev.to/luuuc/i-recorded-my-agent-auditing-a-36k-file-rails-app-the-play-by-play-10h3) (dev.to)
- 2026-07-04 — [Solidus is six gems working as one store. Can an AI map all six at once?](https://medium.com/@lucdiallo/solidus-is-six-gems-working-as-one-store-can-an-ai-map-all-six-at-once-2edf4a4b6f08) (Medium)
- 2026-07-05 — [Your AI has the Rails source memorized. The interesting part is what it still can't see.](https://medium.com/@lucdiallo/your-ai-has-the-rails-source-memorized-the-interesting-part-is-what-it-still-cant-see-d64c198213e4) (Medium)
- 2026-07-06 — ["Ruby is the most AI-friendly stack" is half true](https://dev.to/luuuc/ruby-is-the-most-ai-friendly-stack-is-half-true-1ac4) (dev.to)
- 2026-07-06 — [When is an AI agent good enough on its own? Lobsters marks the exact line.](https://medium.com/@lucdiallo/when-is-an-ai-agent-good-enough-on-its-own-lobsters-marks-the-exact-line-efa634c95d64) (Medium)
- 2026-07-07 — [Redmine has run Ruby's own bug tracker for years. Can an AI navigate it?](https://medium.com/@lucdiallo/redmine-has-run-rubys-own-bug-tracker-for-years-can-an-ai-navigate-it-6be56cc9e06c) (Medium)
- 2026-07-08 — [I pointed my code map at RubyLLM and it had almost nothing to do. That's the point.](https://medium.com/@lucdiallo/i-pointed-my-code-map-at-rubyllm-and-it-had-almost-nothing-to-do-thats-the-point-40af5fe0e356) (Medium)
- 2026-07-09 — [Your next model upgrade won't close this gap](https://dev.to/luuuc/your-next-model-upgrade-wont-close-this-gap-4oi5) (dev.to)
- 2026-07-09 — [The AI read langchainrb cold. Sense still cut the cost 22%.](https://medium.com/@lucdiallo/the-ai-read-langchainrb-cold-sense-still-cut-the-cost-22-f0271c6267a1) (Medium)
- 2026-07-10 — [Same answer, a fifth fewer tokens. Sense on llm.rb.](https://medium.com/@lucdiallo/same-answer-a-fifth-fewer-tokens-sense-on-llm-rb-470d85b2b7f5) (Medium)
- 2026-07-11 — [The AI read every file in raix and still missed the wiring. Sense traced it.](https://medium.com/@lucdiallo/the-ai-read-every-file-in-raix-and-still-missed-the-wiring-sense-traced-it-d27e2b5a3d30) (Medium)
- 2026-07-12 — [How would you test whether an AI understands your codebase?](https://dev.to/luuuc/how-would-you-test-whether-an-ai-understands-your-codebase-1c9l) (dev.to)
- 2026-07-12 — [AI can write Ruby. Can it navigate Ruby?](https://medium.com/@lucdiallo/ai-can-write-ruby-can-it-navigate-ruby-79722bfa99af) (Medium)
- 2026-07-13 — [Sense gives your AI agent superpowers. I spent two weeks trying to prove it doesn't.](https://dev.to/luuuc/sense-gives-your-ai-agent-superpowers-i-spent-two-weeks-trying-to-prove-it-doesnt-5gn6) (dev.to)
- 2026-07-13 — [Sense Gives Your AI Agent Superpowers](https://medium.com/@lucdiallo/sense-gives-your-ai-agent-superpowers-8768b5b4d361) (Medium)
