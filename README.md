[![CI](https://github.com/luuuc/sense/actions/workflows/ci.yml/badge.svg)](https://github.com/luuuc/sense/actions/workflows/ci.yml)  [![CodeQL](https://github.com/luuuc/sense/actions/workflows/codeql.yml/badge.svg)](https://github.com/luuuc/sense/actions/workflows/codeql.yml) [![Go Report Card](https://goreportcard.com/badge/github.com/luuuc/sense)](https://goreportcard.com/report/github.com/luuuc/sense) [![codecov](https://codecov.io/gh/luuuc/sense/branch/main/graph/badge.svg)](https://codecov.io/gh/luuuc/sense) [![OpenSSF Best Practices](https://www.bestpractices.dev/projects/12729/badge)](https://www.bestpractices.dev/projects/12729)

# Sense ⠎⠑⠝⠎⠑

**Codebase understanding for your AI.**

Sense is not a tool you use. It's a tool your AI uses. You install a binary, add one line to your MCP config, and your AI gets the structural understanding of your codebase that a senior engineer carries in their head. You notice it in the absence of frustration — faster answers, fewer wrong turns, code that matches your conventions.

One binary, one index, four tools for your AI. No SaaS account, no API key, no cloud dependency.

> Sense sits on your machine, has no learning curve, and isn't for you — it's for your AI.

## What changes

Measured across 7 real-world codebases (Discourse, Flask, Next.js, Axum, Gin, Javalin, Maket).
Full methodology and raw data: [`bench/`](bench/).

![Side-by-side comparison: Without Sense (19 tool calls, 356K tokens, 121s) vs With Sense (5 tool calls, 109K tokens, 48s)](docs/comparison.svg)

| Metric | Claude Code (Opus 4.6) | Claude Code (Opus 4.6) + Sense | Change |
|---|---|---|---|
| Tool calls per task | 19 | 10 | -47% |
| Tokens per task | 228K | 156K | -32% |
| Cost per task | $0.42 | $0.31 | -26% |
| Session time | 91s | 73s | -19% |
| Score per 100K tokens | 0.19 | 0.30 | +64% |
| Score per minute | 0.28 | 0.38 | +37% |

Same correctness, dramatically less work. Sense doesn't make the model smarter — it gives the model structural understanding so it stops wasting effort.

### Structural tasks

Tasks that require understanding code relationships — not just reading text — are where Sense pulls ahead.

| Task type | Baseline | + Sense | Why |
|---|---|---|---|
| Blast radius | 0.17 | 0.25 | Pre-computed dependency graph vs. manual grep chains |
| Find callers | 0.27 | 0.33 | Graph lookup vs. reading dozens of files |
| Dead code | 0.00 | 0.05 | Baseline can't do this at all |
| Semantic search | 0.36 | 0.38 | Two-stage retrieval (bi-encoder + cross-encoder) with text fallback |

### Where Sense doesn't help

Sense is structural understanding, not a general search engine. For tasks that are fundamentally text-grep (find a log message, locate a string literal), plain grep is the right tool and Sense adds nothing. Search text fallback (ripgrep) bridges some of this gap, but it's a fallback — not a replacement.

## Install

```bash
curl -fsSL https://luuuc.github.io/sense/install.sh | sh
```

Or download the binary for your OS from the [latest release](https://github.com/luuuc/sense/releases/latest), unzip, and move `sense` somewhere on your `PATH`.

### With Go (1.25+)

```bash
go install github.com/luuuc/sense/cmd/sense@latest
```

## Index Your Codebase

```bash
cd /path/to/project && sense scan
```

Parses your code with tree-sitter, extracts symbols and relationships, embeds everything with a bundled ONNX model, and writes a local `.sense/` index. Incremental on every run.

## Connect Your AI

```bash
cd /path/to/project && sense setup
```

Auto-detects installed AI tools (Claude Code, Cursor, Codex CLI) and writes integration configs:

- **`.mcp.json`** — MCP server config (Claude Code, Cursor, any MCP client)
- **`.claude/settings.json`** — lifecycle hooks that nudge Claude toward Sense tools
- **`CLAUDE.md`** — routing guidance with a substitution table
- **`.claude/skills/`** — workflow skills for exploration, impact analysis, and conventions

No manual setup. Run `sense setup` and your AI has structural understanding.

Sense also generates `.sense/summary.md` — a cold-start map of your codebase (top namespaces, hub symbols, entry points, conventions). Your AI reads it at session start and immediately knows the shape of the project. Zero tool calls to orient.

To re-configure after upgrading Sense:

```bash
sense setup
```

## Setup & forget

After `sense setup`, there's nothing left to do. The index updates automatically as your code changes. The summary regenerates on every scan. Your AI gets faster answers and burns fewer tokens — you just stop noticing the friction that used to be there.

## What Your AI Gets

Four tools for your AI. A full CLI for you. No sprawl.

| Tool | Capability |
|---|---|
| `sense_graph` | Symbol relationships, callers, callees, inheritance, tests, dead code |
| `sense_search` | Hybrid semantic + keyword search with text fallback |
| `sense_blast` | Blast radius, affected code, affected tests, risk score |
| `sense_conventions` | Detected project conventions from source |

Your AI stops reading 30 files to answer "who calls this?" It stops hallucinating dependencies. It stops writing code that's correct but doesn't match how your team writes code.

### Convention detection

Of the four tools, convention detection is the one nobody else does well. AI tools don't just struggle with structure — they struggle with style. They write correct code that doesn't follow how YOUR codebase writes code.

Sense detects patterns from your actual source code: key types and their declarations, framework idioms (Rails associations, Go interfaces, Django models), architectural layers, and naming conventions. Your AI follows these patterns because it sees them, not because it was told about them. Convention detection isn't a feature — it's the thing that makes AI-written code feel like it belongs.

## How It Works

Sense parses your codebase with tree-sitter, extracts symbols (functions, classes, modules, methods) and their relationships (calls, imports, inheritance), embeds each symbol with a bundled quantized ONNX model, and stores everything in a local SQLite index at `.sense/`.

```bash
cd /path/to/project && sense scan
```

From that moment on, your AI can ask structural questions via MCP. These are what your AI calls. You can run them manually for verification — see [CLI.md](CLI.md) for the full reference.

### Performance

Sense disappears into your workflow. Queries resolve in milliseconds, so your AI reasons about structure without ever stalling.

| Operation | Measured (p50 / p95) |
|---|---|
| Graph query | 0.2ms / 3ms |
| Blast radius | 0.1ms / 10ms |
| Conventions | 16ms / 16ms |
| Cold start | 48ms |
| Full scan | 4.9s |
| Incremental scan | 2.3s |

Measured on Sense's own codebase (382 files, 4,032 symbols). Run `sense benchmark` on your project for local numbers.

## What Sense Is Not

- **Not a code editor or modifier.** Read-only is the identity, not a limitation. Sense observes your codebase. It never modifies it. Your editor, your agent, your tools stay in control.
- **Not a token optimizer.** Token savings are a side effect of understanding, not the goal. If LLM costs dropped to zero tomorrow, Sense would still be valuable.
- **Not a search engine.** Semantic search is one of four tools, not the product. The product is structural understanding.
- **Not a feature-count competitor.** Four tools is a choice, not a constraint. Your AI doesn't need 102 tools to choose from. It needs a few that work.
- **Not dependent on anything.** No API keys. No Ollama. No Docker. No Python. One binary, zero external dependencies.

## Supported Platforms

| Platform | Status |
|---|---|
| Linux amd64 | Supported |
| Linux arm64 | Supported |
| macOS Apple Silicon (arm64) | Supported |
| macOS Intel (amd64) | Supported |
| Windows | Supported using WSL2 |

Windows native builds are not yet available. Use WSL2 with the Linux binary.

## Requirements

- ~60 MB disk for the binary
- 100-200 MB for the `.sense/` index (varies with project size)

## Language Support

Sense uses tree-sitter for parsing. It ships with extractors for 13 languages across two tiers:

**Full tier** — symbols, calls, inheritance, imports, blast radius, semantic search, and framework-specific inference:

| Language | Framework support |
|---|---|
| **Ruby** | Rails (associations, callbacks, routes), Stimulus, Turbo |
| **TypeScript / JavaScript** | React (JSX component calls) |
| **Python** | Django (models, URL patterns), FastAPI (routes, Depends) |
| **Go** | — |
| **Rust** | — |
| **ERB** | Stimulus, Turbo (cross-language edges to JS controllers) |

**Standard tier** — symbols, calls, inheritance, imports, blast radius, and semantic search (no framework inference):

| Language | Notes |
|---|---|
| **Java** | Classes, interfaces, enums, records |
| **Kotlin** | Classes, interfaces, objects |
| **C#** | Classes, interfaces, structs, namespaces |
| **C++** | Classes, structs, namespaces (`::` scoping) |
| **C** | Functions, structs, enums |
| **PHP** | Classes, interfaces, traits (`\` scoping) |
| **Scala** | Classes, traits, objects |

Standard-tier languages use a table-driven generic extractor — each is ~25 lines of config, not a handwritten walker. See [LANGUAGES.md](LANGUAGES.md) for how to add a new language or framework.

## Feedback

File issues at [github.com/luuuc/sense/issues](https://github.com/luuuc/sense/issues).

## Development

```bash
make build    # build the binary
make test     # run tests
make lint     # run linters
make ci       # all of the above
```

## License

O'Saasy. MIT-style with SaaS-competition rights reserved. See [LICENSE](LICENSE).
