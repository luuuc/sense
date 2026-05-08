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

Find dead code (symbols with no incoming references).

```bash
sense dead
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
