# Contributing to Sense

## What we accept

Sense is feature-complete for v1. To keep it focused and maintainable, outside
contributions are accepted in **three areas only**:

1. **New languages and frameworks.** A tree-sitter-backed language, or framework
   support on top of an existing one. Start with
   [`CONTRIBUTING-A-LANGUAGE.md`](CONTRIBUTING-A-LANGUAGE.md); for a framework on
   a language Sense already parses, go straight to
   [`CONTRIBUTING-A-FRAMEWORK.md`](CONTRIBUTING-A-FRAMEWORK.md).
2. **Dead-code fine-graining.** Teaching the dead-code analyzer about a language
   or framework's invisible-reach idioms (decorators, routes, associations, FFI
   exports) so those symbols are not falsely flagged dead. This is one half of
   framework support and lives in the dead-code section of
   [`CONTRIBUTING-A-FRAMEWORK.md`](CONTRIBUTING-A-FRAMEWORK.md).
3. **AI-tool integration.** Adding or tuning an AI coding tool (Claude Code,
   Cursor, Codex, Opencode, Aider, ...). See
   [`CONTRIBUTING-AN-AI-TOOL.md`](CONTRIBUTING-AN-AI-TOOL.md).

Bug fixes for existing behavior are always welcome.

**Everything else we are not taking**, even if well-built: new commands, new
query or output formats, configuration knobs, performance rewrites, dependency
swaps, and net-new features. The query surface and CLI are deliberately small
and considered done. Please open an issue before investing time in anything
outside the three lanes above; an unsolicited feature PR will be closed with
thanks rather than merged. This is not a judgement of the work, only of scope.

## Dev setup

Requires Go 1.25+.

```bash
git clone https://github.com/luuuc/sense.git
cd sense
./scripts/fetch-deps.sh --local
make build
make test
```

`fetch-deps.sh --local` downloads the ONNX Runtime library and embedding model for your current platform. This is required before the first build.

Note: `.claude/` and `CLAUDE.md` are local-only files excluded from version control. It's your personal settings. Don't commit them.

## Before pushing

```bash
make ci       # build + cover (with coverage floor) + lint
make smoke    # contributor regression smoke test
```

Both must pass. CI invokes the same `make` targets, so a green local `make ci` is a green remote build — they cannot drift. `make smoke` runs `sense scan` and `sense graph` against a fixture project — if it fails, the output will show which command broke.

## Quality gates and the inline ledger

CI enforces five mechanical gates, all reproducible locally:

- **Format** — `gofmt` + `goimports` (`-local github.com/luuuc/sense`) via golangci-lint's `formatters:`. Run `make fmt` to auto-fix; `make lint` rejects unformatted code.
- **Complexity** — `gocyclo` (over 15) and `gocognit` (over 30) on production code in the packages the current cleanup cycle covers. No `funlen` (raw line count is noise). A function over threshold must be **decomposed**, not suppressed — but while the cleanup is in flight, today's known violators carry an inline directive that names the pitch retiring them:

  ```go
  //nolint:gocyclo // 27-07: retired by the storage/query split
  func Compute(...) { ... }
  ```

  This ledger is the burndown, and it is now at its terminal value: **zero**. `grep -rnE 'nolint:goc(yclo|ognit)' internal cmd` returns nothing, `make ledger` enforces `LEDGER_MAX = 0`, so a new `//nolint:gocyclo`/`gocognit` reds CI — decompose the function, do not suppress it. A stray directive left on a now-simple function is itself flagged by `nolintlint`, so the ledger cannot quietly lie.
- **Side effects** — `depguard` forbids the verified pure-core packages (`extract`, `blast`, `conventions`, `mcpio`, `model`) from importing `os/exec`/`net`/`syscall`/`fsnotify`; effects belong at the edges. Pair it with `make test-hermetic`, which runs the hermetic package set offline (no network/ONNX/external binary).
- **Coverage** — `make cover` runs the race suite once and applies two floors over the profile. The **primary** is a per-file gate (`scripts/coveragegate`): **every** production file in the tree must hold ≥92% line **and** function coverage. The gate is deny-by-default — a file is gated unless it is a `_test.go` file, a justified `stragglerException` (one line, with a `PERMANENT`/`DEFERRED` reason), or lives in a pinned `excludedDir` (test-support packages and the gate's own tooling). Code you add to a **new** package is gated automatically; there is no allow-list to opt into. A coarse total-% **backstop** (`COVER_FLOOR`) catches gross regression. Codecov is informational only. Don't lower the floor or grow the exception list to make a change pass — cover the gap, or, only if the residual is genuinely unreachable, add a justified exception.

Do not lower a gate to make a change pass. Decompose the function, inject the effect behind a seam, or add the test.

## Branches

- `feat/<slug>` — new capability
- `fix/<slug>` — bug fix
- `chore/<slug>` — infrastructure, tooling, docs

## Commits

[Conventional commits](https://www.conventionalcommits.org/). Imperative mood, subject line ≤72 characters.

```
feat(graph): add inheritance edge support
fix(scan): handle symlinked directories
chore(ci): pin golangci-lint version
```

## Pull requests

Fill out the PR template: what changed, how you tested, and which issue (if any) the work addresses.

## The contribution guides

Each accepted lane has its own step-by-step guide, written to be followed
literally by an AI agent or a human with no prior knowledge of the codebase:

- [`CONTRIBUTING-A-LANGUAGE.md`](CONTRIBUTING-A-LANGUAGE.md) for a new language.
  It covers the standard tier (a ~30-line table-driven declaration) and the full
  tier (a bespoke extractor), from vendoring the tree-sitter grammar to
  generating goldens.
- [`CONTRIBUTING-A-FRAMEWORK.md`](CONTRIBUTING-A-FRAMEWORK.md) for framework
  support on a language Sense already parses, plus the dead-code fine-graining
  that keeps a framework's invisibly-reached symbols out of the dead report.
- [`CONTRIBUTING-AN-AI-TOOL.md`](CONTRIBUTING-AN-AI-TOOL.md) for a new AI coding
  tool integration, or tuning the guidance an existing one receives.

## Larger changes

Sense follows a lightweight [Shape Up](https://basecamp.com/shapeup) workflow internally. If your change is larger than a small bug fix, open an issue to discuss scope before starting work.

