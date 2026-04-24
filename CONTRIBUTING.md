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
make ci       # build + test + lint
make smoke    # contributor regression smoke test
```

Both must pass. CI runs the same checks. `make smoke` runs `sense scan` and `sense graph` against a fixture project — if it fails, the output will show which command broke.

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

