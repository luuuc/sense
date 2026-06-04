---
name: sense
description: Local-only MCP server that gives AI coding agents structural understanding of your codebase — symbol graph, blast radius, semantic search, and auto-detected project conventions. Reduces tool calls and tokens for any task that depends on understanding code relationships rather than reading text.
license: Other
---

# Sense: Codebase Understanding for AI Coding Agents

Sense is an MCP server that gives AI agents the structural understanding of a codebase that a senior engineer carries in their head. It runs as one Go binary with a local SQLite index — no API keys, no SaaS, no cloud calls.

## When to Use This Skill

Use Sense whenever the task depends on understanding code **relationships**, not just reading text:

- "Who calls this function?" / "What does this function call?"
- "What breaks if I change this signature?" (blast radius)
- "Find the implementation of the X pattern" (semantic search)
- "What conventions does this project follow?" (e.g., naming, Rails idioms, Go interface patterns)
- "Find dead code"
- "Show me all routes / handlers / models / controllers"

Do NOT use Sense for:
- Locating a string literal or log message — plain grep is faster.
- Editing code — Sense is read-only by design.

## Setup (one-time per project)

```bash
# Install the binary (macOS / Linux):
curl -fsSL https://luuuc.github.io/sense/install.sh | sh

# In the project root:
sense scan      # builds the .sense/ index (tree-sitter + embeddings)
sense setup     # writes .mcp.json + CLAUDE.md routing + hooks
```

After `sense setup`, your AI tool (Claude Code, Cursor, Codex CLI) has Sense's four tools available via MCP.

## Tools

| Tool | Capability |
|---|---|
| `sense_graph` | Symbol relationships — callers, callees, inheritance, tests, dead code |
| `sense_search` | Hybrid semantic + keyword search with text fallback |
| `sense_blast` | Blast radius, affected code, affected tests, risk score |
| `sense_conventions` | Auto-detected project conventions (Rails, React, Django, Go, naming) |

Plus a generated `.sense/summary.md` at session-start: top namespaces, hub symbols, entry points, conventions — so the AI is oriented before its first tool call.

## How to Call (MCP)

After `sense setup`, the tools are exposed automatically. Prefer them over grep/glob for any structural question:

- "Who calls X?" → `sense_graph symbol="X" direction=callers`
- "What would break if I change Y?" → `sense_blast symbol="Y"`
- "Find code that does Z" → `sense_search query="natural language description"`
- "How does this project define services?" → `sense_conventions`

## Performance

| Operation | p50 / p95 |
|---|---|
| Graph query | 0.2 ms / 3 ms |
| Blast radius | 0.1 ms / 10 ms |
| Conventions | 16 ms / 16 ms |
| Cold start | 48 ms |
| Full scan | 4.9 s |
| Incremental scan | 2.3 s |

Measured on Sense's own codebase (382 files, 4,032 symbols).

## Why Sense Helps Agents

Across 7 real-world codebases (Discourse, Flask, Next.js, Axum, Gin, Javalin, and a private e-commerce repo), Claude Code with Sense used **47% fewer tool calls** and **32% fewer tokens** per task at the same correctness as baseline Claude Code. Against 7 other code-intelligence MCPs (Serena, Probe, GitNexus, GrepAI, ChunkHound, codebase-memory-mcp, TokenSave, Roam), Sense ranks #1 on the head-to-head leaderboard.

Sense doesn't make the model smarter. It gives the model structural understanding so it stops wasting effort.

## Language Support

**Full tier (framework inference):** Ruby + Rails (associations, callbacks, routes, Stimulus, Turbo), TypeScript/JavaScript + React (JSX), Python + Django (models, URLs) + FastAPI (routes, Depends), Go, Rust, ERB.

**Standard tier (symbols + calls + inheritance + imports + blast + search):** Java, Kotlin, C#, C++, C, PHP, Scala.

## Where to Learn More

- Repo: https://github.com/luuuc/sense
- Homepage: https://luuuc.github.io/sense/
- Benchmark methodology: https://github.com/luuuc/sense/tree/main/bench
- Head-to-head leaderboard: https://github.com/luuuc/sense/blob/main/docs/bench-leaderboard.svg
- Adding a language: https://github.com/luuuc/sense/blob/main/CONTRIBUTING-A-LANGUAGE.md
- CLI reference: https://github.com/luuuc/sense/blob/main/CLI.md
