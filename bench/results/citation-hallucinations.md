# Citation hallucinations

Citations the assistant printed that did not resolve against the repo checked out at `run_meta.repo_commit`. **Hallucinated** = line number beyond EOF (made-up number). **Unresolved** = file not in repo, or symbol not within ±5 lines of the cited line.

Not yet folded into the fairness score — see pitch 20-04.

## baseline

_No ungrounded citations._

## gitnexus

### gitnexus/flask  — 18/19 grounded

**Hallucinated**
- `tests/test_async.py:AsyncView.dispatch_request#0` — line 0 out of range (file only 145 lines)

### gitnexus/gin  — 41/42 grounded

**Unresolved**
- `recovery_test.go:extend` — `extend` not found anywhere in recovery_test.go

## probe

### probe/axum  — 66/67 grounded

**Unresolved**
- `serve.rs:Now` — file not found at serve.rs

## sense

_No ungrounded citations._

## serena

### serena/nextjs  — 41/42 grounded

**Unresolved**
- `render.tsx:renderToHTML:454` — `renderToHTML` not within ±5 of line 454 in render.tsx [via packages/next/src/server/render.tsx]
