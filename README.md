# Sense ⠎⠑⠝⠎⠑

[![CI](https://github.com/luuuc/sense/actions/workflows/ci.yml/badge.svg)](https://github.com/luuuc/sense/actions/workflows/ci.yml)

**Codebase understanding that any tool can query.**

Sense gives your AI tools structural understanding of your codebase. The kind a senior engineer carries in their head but an LLM has to rebuild from scratch every session.

Your tools query a local graph and get precise answers about what exists, how it connects, what breaks if you change it, and what patterns the project follows.

One binary, one index, four capabilities. No SaaS account, no API key, no cloud dependency.

## Install

```bash
curl -fsSL https://luuuc.github.io/sense/install.sh | sh
```

Or download the binary for your OS from the [latest release](https://github.com/luuuc/sense/releases/latest), unzip, and move `sense` somewhere on your `PATH`.

### With Go (1.25+)

```bash
go install github.com/luuuc/sense/cmd/sense@latest
```

### Verify

```bash
sense version
sense scan        # in any project directory
sense graph User  # query the symbol graph
```

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
- 100–200 MB for the `.sense/` index (varies with project size)

## How It Works

Sense parses your codebase with tree-sitter, extracts symbols (functions, classes, modules, methods) and their relationships (calls, imports, inheritance), embeds each symbol with a bundled quantized ONNX model, and stores everything in a local SQLite index at `.sense/`.

```bash
cd /path/to/project && sense scan
```

From that moment on, your AI tools can ask structural questions:

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

### MCP Tools

| Tool | Capability |
|---|---|
| `sense.graph` | Symbol relationships, callers, callees, inheritance, tests |
| `sense.search` | Hybrid semantic + keyword search |
| `sense.blast` | Blast radius, affected code, affected tests, risk score |
| `sense.conventions` | Detected project conventions |
| `sense.status` | Index health, coverage, staleness, last scan |

Four capabilities, plus `sense.status` for index health. Focused, composable, no sprawl.

## MCP Setup

Add to your `.mcp.json` (Claude Code, Cursor, or any MCP-speaking tool):

```json
{
  "mcpServers": {
    "sense": {
      "command": "sense",
      "args": ["mcp"]
    }
  }
}
```

Cursor users: place the same block in `~/.cursor/mcp.json`.

## Claude Code Setup

After connecting Sense via `.mcp.json`, add the following to your project's `CLAUDE.md` so Claude Code uses Sense proactively:

```markdown
## Sense (codebase understanding)

Sense is connected as an MCP server. Load its tools via ToolSearch at the start
of any code exploration task. Call `sense.status` first to confirm the index is
healthy; fall back to grep/glob only if Sense is unavailable or the index is stale.

### Before writing code

1. `sense.status` — confirm index health.
2. `sense.conventions` — check patterns for the domain you're working in.
3. `sense.search` — look for prior art before creating new code.
4. `sense.blast` — check scope of the symbols you're about to change.

### While writing code

- `sense.graph` — check callers before modifying a symbol's signature.
- `sense.search` — check for existing implementations before creating new ones.

### After completing work

- `sense.blast --diff HEAD~1` — verify the scope of your changes.
```

## Language Support

| Tier | Languages | Coverage |
|---|---|---|
| **Tier 1 (Full)** | Ruby, Go, TypeScript, JavaScript | Full graph + framework-aware extractors (Rails, Next.js, stdlib Go) |
| **Tier 2 (Standard)** | Python, Java, Rust | Full graph, no framework-specific inference |
| **Tier 3 (Basic)** | C/C++, PHP, Elixir, Swift, Kotlin | Symbol + call graph, no inheritance inference |

New Tier 1 languages are added by writing a framework-aware extractor on top of the base tree-sitter graph.

## What Sense Brings

Instead of reading dozens of files to build a mental model, your AI queries a graph and gets precise answers.

- **Meaning over strings.** Your AI reasons over actual structure instead of pattern-matching file contents, so it makes fewer wrong assumptions about what connects to what.
- **Derived, not curated.** The graph rebuilds from your code automatically. No ontology to maintain, no config to tune.
- **Read-only by design.** Sense observes the codebase. It never modifies it. Your editor and your tools stay in control.
- **Four capabilities, full stop.** Symbol graph, semantic search, blast radius, convention detection. Sense does these cleanly and resists the gravity toward "do everything."

Token savings? A natural by-product. But the real gain is that your AI stops guessing at structure and starts knowing it.

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
