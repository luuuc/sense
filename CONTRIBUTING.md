# Contributing to Sense

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

  This ledger is the burndown: `grep -rnE 'nolint:goc(yclo|ognit)' internal cmd` lists every outstanding entry, and `make ledger` fails CI if the count grows past its cap (`LEDGER_MAX` in the Makefile). The exit condition is the cap reaching zero. A stray directive left on a now-simple function is itself flagged by `nolintlint`, so the ledger cannot quietly lie.
- **Side effects** — `depguard` forbids the verified pure-core packages (`extract`, `blast`, `conventions`, `mcpio`, `model`) from importing `os/exec`/`net`/`syscall`/`fsnotify`; effects belong at the edges. Pair it with `make test-hermetic`, which runs the hermetic package set offline (no network/ONNX/external binary).
- **Coverage** — `make cover` runs the race suite and fails below the total-% floor (`COVER_FLOOR`). Codecov is informational only.

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

## Larger changes

Sense follows a lightweight [Shape Up](https://basecamp.com/shapeup) workflow internally. If your change is larger than a small bug fix, open an issue to discuss scope before starting work.

