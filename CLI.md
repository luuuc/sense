# Sense CLI Reference

Sense is designed for your AI through its MCP interface. The CLI exists for three purposes: initial setup, health checks, and manual exploration when needed.

## MCP-first design

Your AI uses Sense automatically through these tools:

| Tool | Purpose |
|---|---|
| `sense_graph` | Symbol relationships — callers, callees, inheritance, tests |
| `sense_search` | Hybrid semantic + keyword search |
| `sense_blast` | Blast radius for a symbol or diff |
| `sense_conventions` | Detected project conventions |

The CLI commands below mirror some of these capabilities for manual use, but the primary interface is MCP.

---

## Setup commands

### `sense setup`

Configure AI tool integrations. Auto-detects installed tools (Claude Code, Cursor, Codex CLI) and writes integration files.

```bash
sense setup                           # auto-detect and configure all
sense setup --tools cursor            # configure Cursor only
sense setup --tools claude-code,codex-cli
```

### `sense scan`

Build or refresh the index.

```bash
sense scan              # index the current directory
sense scan --watch      # keep running and re-index on file changes
sense scan --embed      # block until embeddings complete
```

---

## Health commands

### `sense status`

Show index health: file/symbol/edge counts, per-language breakdown, coverage, freshness, and version info.

```bash
sense status        # human-readable summary
sense status --json # machine-readable output
```

### `sense doctor`

Diagnose common index problems.

```bash
sense doctor
```

---

## Exploration commands

### `sense search`

Hybrid semantic + keyword search.

```bash
sense search "retry with backoff"
sense search "payment error handling" --min-score 0.7
```

### `sense graph`

Symbol relationships.

```bash
sense graph "CheckoutService"          # callers and callees
sense graph "User" --direction callers # callers only
sense graph --dead                     # find dead code
```

### `sense blast`

Blast radius for a symbol or diff.

```bash
sense blast "User#email_verified?"     # symbol-based
sense blast --diff HEAD~1              # diff-based
```

### `sense dead`

Find unreferenced symbols (zero incoming references), split into honest
verdicts rather than a flat "dead" list:

- **`dead`** — safe to remove. Reserved for the rare symbol Sense can reason
  about *closed-world*: every possible caller is visible to the indexer (e.g. a
  private Ruby method whose name is never a reflection-dispatch target), so a
  zero-reference result is a real zero, not a gap in the graph. As a soundness
  guard against an incomplete resolver, `dead` is withheld unless the symbol's
  bare name is also *mentioned nowhere* in the index it could be an unresolved
  caller — an inherited bare call, a `**splat`, a chain receiver, or a
  `validate :sym`-style symbol argument all leave a textual mention that keeps
  the symbol `possibly_dead` instead. Each `dead` still carries a per-symbol
  `verify` grep — a final cheap check against the live tree, since the index can
  lag the working copy.
- **`possibly_dead`** — unreferenced, but a hidden caller could exist
  (duck-typed dispatch, routes, views, public API, …). The default and
  majority verdict, grouped by reason, each group with a `verify` recipe.

A symbol earns `dead` only when a language voice can prove closed-world for
its language; a stack with no voice (today: anything but Ruby) is always
`possibly_dead`, so Sense never emits a confident lie on an unsupported stack.

```bash
sense dead                     # human-readable verdicts
sense dead --language ruby     # filter by language
sense dead --domain services   # filter by path substring
sense dead --json              # JSON matching the sense_graph dead_code schema
sense dead --limit 50          # cap reported symbols (dead is never truncated)
```

### `sense conventions`

Detected project conventions.

```bash
sense conventions          # all conventions
sense conventions --domain errors
```

---

## Utility commands

| Command | Purpose |
|---|---|
| `sense mcp` | Start the MCP server (stdio transport) |
| `sense update` | Check for and install the latest version |
| `sense version` | Print version |
| `sense benchmark` | Run performance benchmarks on the index |

---

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General error |
| 2 | Invalid arguments |
| 3 | Index missing (run `sense scan` first) |
