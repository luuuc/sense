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

## Benchmarking AI on Ruby and Rails (in progress)

How well does an AI agent navigate real Ruby and Rails code, with and without a structural map? This series runs the same maintainer task on each repo twice, plain Claude Code as the baseline and Claude Code plus Sense, on an open and reproducible harness, then reports where Sense earns its place and where it does not. It spans 13 codebases, from large Rails apps down to the Ruby AI-stack gems. Links are added here as each piece publishes.
