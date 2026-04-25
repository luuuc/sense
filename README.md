[![CI](https://github.com/luuuc/sense/actions/workflows/ci.yml/badge.svg)](https://github.com/luuuc/sense/actions/workflows/ci.yml)

# Sense ⠎⠑⠝⠎⠑

**Codebase understanding for your AI.**

Sense is not a tool you use. It's a tool your AI uses. You install a binary, add one line to your MCP config, and your AI gets the structural understanding of your codebase that a senior engineer carries in their head. You notice it in the absence of frustration — faster answers, fewer wrong turns, code that matches your conventions.

One binary, one index, four capabilities. No SaaS account, no API key, no cloud dependency.

> Sense sits on your machine, has no learning curve, and isn't for you — it's for your AI.

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

The first `sense scan` automatically configures your AI tools:

- **`.mcp.json`** — MCP server config (Claude Code, Cursor, any MCP client)
- **`.claude/settings.json`** — lifecycle hooks that nudge Claude toward Sense tools
- **`CLAUDE.md`** — routing guidance with a substitution table
- **`.claude/skills/`** — workflow skills for exploration, impact analysis, and conventions

No manual setup. Run `sense scan` and your AI has structural understanding.

Cursor users: copy the `sense` entry from `.mcp.json` into `~/.cursor/mcp.json`.

To re-generate config files after upgrading Sense:

```bash
sense scan --init
```

## What Your AI Gets

Four capabilities. No sprawl.

| Tool | Capability |
|---|---|
| `sense.graph` | Symbol relationships, callers, callees, inheritance, tests |
| `sense.search` | Hybrid semantic + keyword search |
| `sense.blast` | Blast radius, affected code, affected tests, risk score |
| `sense.conventions` | Detected project conventions |
| `sense.status` | Index health, coverage, staleness, last scan |

Your AI stops reading 30 files to answer "who calls this?" It stops hallucinating dependencies. It stops writing code that's correct but doesn't match how your team writes code.

### Convention detection

Of the four capabilities, convention detection is the one nobody else does well. AI tools don't just struggle with structure — they struggle with style. They write correct code that doesn't follow how YOUR codebase writes code.

Sense detects patterns: that all your models inherit `ApplicationRecord`, all your services follow the command pattern, all your tests use fixtures. Your AI follows these patterns automatically. Convention detection isn't a feature — it's the thing that makes AI-written code feel like it belongs.

## How It Works

Sense parses your codebase with tree-sitter, extracts symbols (functions, classes, modules, methods) and their relationships (calls, imports, inheritance), embeds each symbol with a bundled quantized ONNX model, and stores everything in a local SQLite index at `.sense/`.

```bash
cd /path/to/project && sense scan
```

From that moment on, your AI can ask structural questions via MCP:

```bash
sense graph "CheckoutService"
# => CheckoutService (app/services/checkout_service.rb:12)
#    calls: PaymentGateway.charge, Order.finalize
#    called by: OrdersController#create, CheckoutJob#perform

sense blast "User#email_verified?"
# => Direct callers (4), indirect (11), affected tests (6)
#    Risk: MEDIUM (hub node, touches auth + admin)

sense search "error handling for payment failures"
# => app/services/payment_gateway.rb:45  (0.92)
#    app/controllers/orders_controller.rb:78  (0.87)

sense conventions
# => Service objects: 12 found, all inherit ApplicationService
#    Test pattern: Minitest, fixtures, no DB mocking
```

These are what your AI calls. You can run them manually for verification, but the primary interface is MCP.

### Performance

Sense must be fast enough to be invisible. If your AI pauses noticeably while querying Sense, you notice Sense exists — and that's a failure.

| Operation | Target |
|---|---|
| Graph queries | < 10ms |
| Semantic search | < 50ms |
| Cold start | < 100ms |

## What Sense Is Not

- **Not a code editor or modifier.** Read-only is the identity, not a limitation. Sense observes your codebase. It never modifies it. Your editor, your agent, your tools stay in control.
- **Not a token optimizer.** Token savings are a side effect of understanding, not the goal. If LLM costs dropped to zero tomorrow, Sense would still be valuable.
- **Not a search engine.** Semantic search is one of four capabilities, not the product. The product is structural understanding.
- **Not a feature-count competitor.** Four capabilities is a choice, not a constraint. Your AI doesn't need 102 tools to choose from. It needs four that work.
- **Not dependent on anything.** No API keys. No Ollama. No Docker. No Python. One binary, zero external dependencies.

## Supported Platforms

| Platform | Status |
|---|---|
| Linux amd64 | Supported |
| Linux arm64 | Supported |
| macOS Apple Silicon (arm64) | Supported |
| macOS Intel (amd64) | Supported |
| Windows | Not supported (use WSL2) |

Windows native builds are not yet available. Use WSL2 with the Linux binary.

## Requirements

- ~60 MB disk for the binary
- 100-200 MB for the `.sense/` index (varies with project size)

## Language Support

Sense uses tree-sitter for parsing. It ships with extractors for six languages and understands popular frameworks out of the box:

| Language | Framework support |
|---|---|
| **Ruby** | Rails (associations, callbacks, routes), Stimulus, Turbo |
| **TypeScript / JavaScript** | React (JSX component calls) |
| **Python** | Django (models, URL patterns), FastAPI (routes, Depends) |
| **Go** | — |
| **Rust** | — |
| **ERB** | Stimulus, Turbo (cross-language edges to JS controllers) |

All six get the full toolkit: symbols, calls, inheritance, blast radius, and semantic search. Adding a new language takes ~100 lines of Go on top of its tree-sitter grammar.

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
