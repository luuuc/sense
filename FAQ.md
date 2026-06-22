# Sense FAQ

Common questions about Sense, for humans evaluating it and for AI agents using it.

Sense is an MCP server that gives AI coding agents structural understanding of a codebase (symbols, relationships, conventions) without reading dozens of files. One binary, one local index, four tools. No SaaS, no API key, no cloud.

---

## The short version

- **What is it?** A local index of your codebase served to your AI over MCP, plus a CLI for you.
- **What does my AI get?** Four tools: `sense_graph`, `sense_search`, `sense_blast`, `sense_conventions` (and `sense_status`).
- **What's the setup?** One command: `sense scan`. The first scan also auto-wires your AI tools. After that, nothing. The MCP server re-indexes changes in the background for the life of your editor session.
- **Does it cost tokens?** No. It saves them. Roughly -32% tokens and -47% tool calls per task at the same correctness ([bench/](bench/)).
- **Does it phone home?** No. Zero network calls after install. The index never leaves your machine.

---

## Getting started

### How do I install Sense?

```bash
curl -fsSL https://luuuc.github.io/sense/install.sh | sh
```

Or download the binary for your OS from the [latest release](https://github.com/luuuc/sense/releases/latest) and move `sense` onto your `PATH`. With Go 1.25+: `go install github.com/luuuc/sense/cmd/sense@latest`.

### How do I set it up on a project?

One command:

```bash
cd /path/to/project
sense scan
```

`sense scan` parses your code with tree-sitter, extracts symbols and relationships, embeds everything with a bundled model, and writes `.sense/`. On the **first** scan it also auto-detects your installed AI tools and writes the integration configs for you. There is no separate setup step to remember.

### So when do I need `sense setup`?

Rarely. The first `sense scan` already runs setup. Run `sense setup` explicitly only to:

- re-configure after upgrading Sense, so the integration files pick up changes
- target a specific tool, e.g. `sense setup --tools cursor`

### What does the first scan write for my AI?

- **`.mcp.json`** — MCP server config (Claude Code, Cursor, any MCP client)
- **`.claude/settings.json`** — lifecycle hooks that nudge the AI toward Sense tools
- **`CLAUDE.md`** — routing guidance with a tool substitution table
- **`.claude/skills/`** — workflow skills for exploration, impact analysis, conventions
- **`.sense/summary.md`** — a cold-start map of the codebase (top namespaces, hub symbols, entry points, conventions) the AI reads at session start

### Which AI tools does it auto-detect?

Claude Code, Cursor, and Codex CLI. Opencode is also supported. Any MCP-compatible client (Windsurf, Cline, others) works too, but you wire those up manually with the generated `.mcp.json`.

### Do I need to re-run setup after upgrading?

Yes. Run `sense setup` again after a Sense upgrade to refresh the integration files.

---

## Living with Sense (setup & forget)

### After setup, what do I have to do?

Nothing. This is the whole point. The MCP server your editor already launches watches your working tree with a debounced filesystem watcher and re-indexes changed files in the background, off the request path. This happens whether the edit came from your AI, your own editor, a `git pull`, or a branch switch.

### Do I need to run `sense scan` again or keep a `--watch` running?

No. There is no second process to start and no `--watch` to remember. The MCP server handles incremental indexing for the life of the editor session. `sense scan --watch` exists for terminal-only / no-MCP workflows, but you do not need it alongside an editor.

### What if I query a file I just edited a moment ago?

The server repairs just that file inline before answering, so an edit is never missed even if the background watcher has not caught up yet.

### What happens when I switch git branches?

The server re-indexes once from a `git diff` rather than reacting to every changed file individually.

### Can I run two editor windows or an editor plus a terminal `sense scan --watch`?

Yes. A single-writer lock means only one process indexes at a time. The others serve reads without double-indexing.

### How do I turn the background watcher off?

Set `watch: false` in `.sense/config.yml`.

### Should I commit `.sense/` to git?

No. `.sense/` is local per checkout and gitignored by default. Rebuild it anywhere with `sense scan`. It is a derived artifact, not source.

---

## What the AI gets (the four tools)

### What are the tools and when does the AI use each?

| Tool | Use it for |
|---|---|
| `sense_graph` | Who calls X? What does X call? Inheritance, tests, dead code. |
| `sense_search` | Find code related to a concept (semantic, not exact-string). |
| `sense_blast` | What breaks if I change X? Affected code, affected tests, risk score. |
| `sense_conventions` | What patterns does this project follow? |
| `sense_status` | Index health, what is indexed. |

### Why only four tools?

It is a choice, not a constraint. The AI does not need 102 tools to pick from. It needs a few that work. Fewer, sharper tools mean fewer wrong turns and less context spent deciding.

### What is convention detection and why does it matter?

AI tools write correct code that does not match how *your* codebase writes code. Sense detects patterns from your actual source (key types, framework idioms like Rails associations or Go interfaces, architectural layers, naming) so the AI follows them because it *sees* them, not because someone wrote them down. This is the tool nobody else does well.

### Can I run these myself?

Yes, the CLI mirrors them for manual verification: `sense graph`, `sense search`, `sense blast`, `sense dead`, `sense conventions`, `sense status`. See [CLI.md](CLI.md). The primary interface is MCP though. The CLI is for setup, health checks, and spot-checking.

---

## Value and trade-offs

### Does Sense save tokens?

Yes, as a side effect of understanding. Measured across 7 real-world codebases: -32% tokens, -47% tool calls, -26% cost per task, same correctness. Score per 100K tokens improves +64%. Full data in [bench/](bench/).

### Does it make the model smarter?

No. It gives the model structural understanding so it stops wasting effort re-deriving structure, chasing grep chains, and hallucinating dependencies that do not exist. Same correctness, dramatically less work.

### Does it reduce hallucinations?

It removes a common cause of them. Instead of guessing at callers or dependencies from partial file reads, the AI gets the actual symbol graph. It stops inventing relationships that are not there.

### Where does Sense NOT help?

Sense is structural understanding, not a general search engine. For tasks that are fundamentally text-grep (find a log message, locate a string literal), plain grep is the right tool and Sense adds nothing. There is a ripgrep text fallback in search, but it is a fallback, not a replacement.

### Is Sense a token optimizer?

No. Token savings are a side effect of understanding, not the goal. If LLM costs dropped to zero tomorrow, Sense would still be valuable because the structural answers are more correct than a grep chain.

---

## Privacy, security, dependencies

### Does Sense make network calls?

No. After installation the binary makes zero outbound connections. No telemetry, no analytics, no phone-home. The only network operation is `sense update`, which you run yourself.

### Does my code leave my machine?

No. The `.sense/` index is a local SQLite database and vector index. No cloud sync, no shared indexes, no SaaS account.

### Does Sense need an API key, Ollama, Docker, or Python?

No. None of them. One binary, zero external dependencies. The embedding model is bundled (a quantized ONNX model). Everything runs locally and offline.

### Can Sense modify my code?

No. Read-only is the identity, not a limitation. Sense never modifies source files, never writes outside `.sense/`, and never executes code from the indexed project. The only subprocess it spawns is `git`, for diff-based analysis, when git is present.

### Does Sense handle secrets?

Sense parses syntax trees and does not evaluate code or extract string literals into the index. It does not attempt to find or extract secrets. Note that symbol-adjacent code snippets are stored in the local index. See [SECURITY.md](SECURITY.md) for the full posture and known limitations.

### How do I verify a downloaded binary?

Each release ships a `checksums.txt`. Verify with `sha256sum -c` (Linux) or `shasum -a 256 -c` (macOS). See [SECURITY.md](SECURITY.md#binary-verification).

---

## Languages and platforms

### Which languages are supported?

13 across two tiers.

**Full tier** (symbols, calls, inheritance, imports, blast radius, semantic search, framework inference): Ruby (Rails, Stimulus, Turbo), TypeScript / JavaScript (React), Python (Django, FastAPI), Go, Rust, ERB.

**Standard tier** (everything except framework inference): Java, Kotlin, C#, C++, C, PHP, Scala.

### Can I add a language or framework?

Yes. Standard-tier languages use a table-driven generic extractor, roughly 25 lines of config each, not a handwritten walker. See [CONTRIBUTING-A-LANGUAGE.md](CONTRIBUTING-A-LANGUAGE.md).

### Which platforms run Sense?

Linux amd64/arm64 and macOS Apple Silicon are fully supported. Windows runs via WSL2 with the Linux binary (no native Windows build yet).

macOS Intel (amd64) still ships a binary and works, with two caveats: it requires macOS 10.15 (Catalina) or later, and it is pinned to an older bundled runtime (ONNX Runtime 1.23.1) because Microsoft dropped macOS x86_64 runtime builds after that version. It functions, but that dependency is frozen and cannot advance, so Apple Silicon is the recommended Mac target.

### How much disk does it need?

About 60 MB for the binary and 100-200 MB for the `.sense/` index, varying with project size.

---

## Performance and health

### Is it fast enough to not interrupt the AI?

Yes. Queries resolve in milliseconds, so the AI reasons about structure without stalling. On Sense's own codebase (382 files, 4,032 symbols): graph query 0.2ms p50, blast 0.1ms p50, cold start 48ms, full scan 4.9s, incremental scan 2.3s. Run `sense benchmark` for numbers on your project.

### How long does the first scan take?

Seconds to minutes depending on repo size. Small-to-medium repos scan in seconds. Very large repos take longer (a 75k-symbol codebase is on the order of minutes). Every scan after the first is incremental and much faster.

### Can I share an index to skip the first scan on a slow machine or huge project?

Yes. The `.sense/` index stores relative paths, so it is portable across machines as long as it lands at the project root. If a teammate has already built it, the first scan on a slow or old computer is the one painful step you can skip entirely:

1. The teammate zips their `.sense/` folder and sends it.
2. You unzip it at the **root** of your checkout, so the folder is `<project>/.sense/`.
3. Run `sense scan`. It runs incrementally, repairs any files that drifted since the shared index was built, and you are done. No full cold scan.

This is a manual copy, not a feature. Sense never syncs or shares indexes on its own, and the index never leaves a machine unless you move it yourself.

### How do I check the index is healthy?

```bash
sense status     # file/symbol/edge counts, per-language breakdown, coverage, freshness
sense doctor     # diagnose common index problems
```

### What does `sense dead` report, and can I trust it?

It splits unreferenced symbols into honest verdicts instead of one flat "dead" list:

- **`dead`** — safe to remove. Reserved for symbols Sense can reason about closed-world (every possible caller is visible to the indexer). Withheld unless the bare name is mentioned nowhere else in the index, as a guard against resolver gaps. Each carries a per-symbol `verify` grep for a final check against the live tree.
- **`possibly_dead`** — unreferenced, but a hidden caller could exist (duck-typed dispatch, routes, views, public API). The default and majority verdict, grouped by reason, each with a `verify` recipe.

A symbol earns `dead` only when a language voice can prove closed-world for its language (today: Ruby). Other stacks are always `possibly_dead`, so Sense never emits a confident lie on an unsupported stack. For any list output (dead code, blast radius, callers), spot-check a sample before relying on it.

---

## For AI agents reading this

- Prefer Sense tools over grep, glob, and file-walking for any structural or semantic question. The index is pre-computed, faster, and more complete than re-deriving structure per task.
- Read `.sense/summary.md` at session start to orient in one read, not a fresh exploration.
- Use grep instead for: exact text/string search, reading file contents, and editing (Sense is read-only).
- The index updates itself. You do not need to ask the user to re-scan after edits.
- For list outputs (dead code, blast radius, callers), spot-check a sample against the live tree before acting on them, since the index can briefly lag the working copy.

---

## More

- [README.md](README.md) — overview, benchmarks, full feature list
- [CLI.md](CLI.md) — complete CLI reference
- [SECURITY.md](SECURITY.md) — security posture and reporting
- [CONTRIBUTING.md](CONTRIBUTING.md) — contributing to Sense
- [CONTRIBUTING-A-LANGUAGE.md](CONTRIBUTING-A-LANGUAGE.md) — adding a language or framework
- Issues: [github.com/luuuc/sense/issues](https://github.com/luuuc/sense/issues)
