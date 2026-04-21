# Sense

**Codebase understanding that any tool can query.**

Sense gives your AI tools structural understanding of the code they're working in. Instead of exploring through repeated grep/glob/read cycles — burning tokens to rediscover what the codebase already knows about itself — your tools query a local graph and get precise answers: what exists, how it connects, what breaks if you change it, and what patterns the project follows.

One binary, one index, four capabilities. No SaaS account, no API key, no cloud dependency.

## Status

Pre-alpha. Graph scanning, symbol queries, blast radius, and MCP server work. Embeddings and semantic search are next.

## Install

### With Go (1.25+)

```bash
go install github.com/luuuc/sense/cmd/sense@latest
```

### Without Go

Download the binary for your OS from the [latest release](https://github.com/luuuc/sense/releases/latest), unzip, and move `sense` somewhere on your `PATH`.

Or use the install script (macOS and Linux, amd64 or arm64):

```bash
curl -fsSL https://raw.githubusercontent.com/luuuc/sense/main/install.sh | sh
```

### Verify

```bash
sense version
sense scan        # in any project directory
sense graph User  # query the symbol graph
```

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

### Five MCP Tools

| Tool | Capability |
|---|---|
| `sense.search` | Hybrid semantic + keyword search |
| `sense.graph` | Symbol relationships — callers, callees, inheritance, tests |
| `sense.blast` | Blast radius — affected code, affected tests, risk score |
| `sense.conventions` | Detected project conventions |
| `sense.status` | Index health — coverage, staleness, last scan |

Five tools. Focused, composable, no sprawl.

## MCP Setup

Add to your `.mcp.json` (Claude Code, Cursor, or any MCP-speaking tool):

```json
{
  "mcpServers": {
    "sense": {
      "command": "sense",
      "args": ["mcp", "--dir", "."]
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

## Token Savings — Measured, Not Claimed

Every MCP response includes `estimated_file_reads_avoided` and `estimated_tokens_saved`. Session analytics via `sense stats`. No telemetry. Numbers stay on your machine.

Sense doesn't compress answers — it lets your tools ask better questions so the wasteful queries never happen.

## What Sense Is Not

- **Not a token optimizer.** Understanding is the identity. Token savings is the side effect.
- **Not a code editor.** Sense reads the codebase. It does not modify it. Read-only by design.
- **Not a replacement for grep.** Use ripgrep for exact text. Sense is for meaning.

## Feedback

Pre-alpha — expect rough edges. File issues at [github.com/luuuc/sense/issues](https://github.com/luuuc/sense/issues).

## Development

```bash
make build    # build the binary
make test     # run tests
make lint     # run linters
make ci       # all of the above
```

## License

O'Saasy — MIT-style with SaaS-competition rights reserved. See [LICENSE](LICENSE).
