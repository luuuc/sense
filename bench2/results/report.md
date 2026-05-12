## Scenario Evaluation

Results: 2 tools × 6 scenarios

### Reading the scores

| Metric | Best | Meaning |
|--------|------|---------|
| score | Higher | Overall scenario score — higher is better |
| completeness | Higher | Checklist completion rate [60%] — higher is better |
| efficiency | Higher | Token efficiency [40%] — higher means fewer tokens per correctness point |
| rich | Higher | Richness — unique source files referenced across all steps |
| grep | Lower | grep/Bash calls — lower means less raw text searching |
| read | Lower | Read/Glob calls — lower means less manual file reading |
| mcp | Higher | MCP tool calls — higher means code intelligence tools were used |
| tokens | Lower | Billed tokens (uncached) — lower is better (cheaper) |
| wall_time | Lower | Wall-clock time — lower is better |
| cost_usd | Lower | API cost in USD — lower is better |

### axum

> Multi-step Axum refactoring: trace Handler trait propagation, understand extractor chaining, add a request ID layer. Tests Rust trait analysis, Tower middleware comprehension, and layered modification.

| Rank | Tool | Score | Comp | Eff | Rich | Tokens | Grep | Read | MCP | Time | Cost |
|-----:|------|------:|-----:|----:|----:|-------:|-----:|-----:|----:|-----:|-----:|
| 1 | baseline :1st_place_medal: | 0.979 | 96% | 1.00 | 15 | 6,749 | 6 | 14 | 0 | 146.6s | $0.79 |
| 2 | sense :2nd_place_medal: | 0.979 | 96% | 1.00 | 18 | 7,064 | 0 | 15 | 4 | 154.1s | $1.01 |

### discourse

> Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.

| Rank | Tool | Score | Comp | Eff | Rich | Tokens | Grep | Read | MCP | Time | Cost |
|-----:|------|------:|-----:|----:|----:|-------:|-----:|-----:|----:|-----:|-----:|
| 1 | sense :1st_place_medal: | 0.959 | 93% | 1.00 | 9 | 6,928 | 8 | 12 | 7 | 123.5s | $0.58 |
| 2 | baseline :2nd_place_medal: | 0.902 | 93% | 0.86 | 8 | 8,500 | 19 | 14 | 0 | 150.6s | $0.65 |

### flask

> Multi-step Flask refactoring: trace WSGI dispatch, locate tests, add a debug parameter, verify the change. Tests call graph traversal, test-file mapping, and safe code modification awareness.

| Rank | Tool | Score | Comp | Eff | Rich | Tokens | Grep | Read | MCP | Time | Cost |
|-----:|------|------:|-----:|----:|----:|-------:|-----:|-----:|----:|-----:|-----:|
| 1 | baseline :1st_place_medal: | 0.932 | 89% | 1.00 | 2 | 5,215 | 7 | 8 | 0 | 93.2s | $0.55 |
| 2 | sense :2nd_place_medal: | 0.932 | 89% | 1.00 | 3 | 5,814 | 3 | 7 | 6 | 116.9s | $0.57 |

### gin

> Multi-step Gin exploration: understand middleware chaining, trace HTTP dispatch, find dead code, modify the recovery middleware. Tests data flow tracing, dead code detection, and structural editing awareness.

| Rank | Tool | Score | Comp | Eff | Rich | Tokens | Grep | Read | MCP | Time | Cost |
|-----:|------|------:|-----:|----:|----:|-------:|-----:|-----:|----:|-----:|-----:|
| 1 | sense :1st_place_medal: | 0.963 | 94% | 1.00 | 8 | 7,556 | 0 | 6 | 5 | 150.5s | $0.66 |
| 2 | baseline :2nd_place_medal: | 0.888 | 94% | 0.81 | 7 | 11,222 | 40 | 19 | 0 | 209.2s | $1.47 |

### javalin

> Multi-step Javalin exploration: understand servlet dispatch, trace routing table construction, add a custom error handler. Tests Java framework comprehension and handler registration patterns.

| Rank | Tool | Score | Comp | Eff | Rich | Tokens | Grep | Read | MCP | Time | Cost |
|-----:|------|------:|-----:|----:|----:|-------:|-----:|-----:|----:|-----:|-----:|
| 1 | baseline :1st_place_medal: | 0.979 | 96% | 1.00 | 6 | 7,438 | 0 | 21 | 0 | 175.3s | $0.62 |
| 2 | sense :2nd_place_medal: | 0.979 | 96% | 1.00 | 10 | 6,598 | 0 | 17 | 11 | 127.2s | $0.65 |

### nextjs

> Multi-step Next.js exploration: trace SSR render path, understand route matching, thread a request ID. Tests TypeScript monorepo navigation and complex server-side pipeline understanding.

| Rank | Tool | Score | Comp | Eff | Rich | Tokens | Grep | Read | MCP | Time | Cost |
|-----:|------|------:|-----:|----:|----:|-------:|-----:|-----:|----:|-----:|-----:|
| 1 | sense :1st_place_medal: | 0.910 | 98% | 0.81 | 18 | 11,174 | 20 | 22 | 8 | 205.5s | $1.26 |
| 2 | baseline :2nd_place_medal: | 0.897 | 98% | 0.78 | 15 | 13,133 | 26 | 31 | 0 | 263.1s | $1.77 |

### Aggregate

| Rank | Tool | Scenarios | Avg Score | Avg Comp | Avg Eff | Avg Tokens | Avg Grep | Avg MCP | Total Cost |
|-----:|------|----------:|----------:|---------:|--------:|-----------:|---------:|--------:|-----------:|
| 1 | sense :1st_place_medal: | 6 | 0.9536 | 0.9433 | 0.9690 | 7,522 | 5.2 | 6.8 | $4.72 |
| 2 | baseline :2nd_place_medal: | 6 | 0.9295 | 0.9433 | 0.9087 | 8,710 | 16.3 | 0.0 | $5.84 |
