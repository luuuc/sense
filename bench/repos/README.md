# Benchmark Repos

Clone these repos locally before running the harness. Pin to exact commits for reproducible ground-truth.

## Repos

| Repo | Language | Clone |
|---|---|---|
| **flask** | Python | `git clone --depth=1 https://github.com/pallets/flask.git repos/flask` |
| **discourse** | Ruby/Rails | `git clone --depth=1 https://github.com/discourse/discourse.git repos/discourse` |
| **openproject** | Ruby/Rails | `git clone --depth=1 https://github.com/opf/openproject.git repos/openproject` |
| **gin** | Go | `git clone --depth=1 https://github.com/gin-gonic/gin.git repos/gin` |
| **maket** | Ruby/Rails | `git clone --depth=1 https://github.com/maket-store/maket-web.git repos/maket` |
| **nextjs** | TypeScript | `git clone --depth=1 https://github.com/vercel/next.js.git repos/nextjs` |
| **javalin** | Java | `git clone --depth=1 https://github.com/javalin/javalin.git repos/javalin` |
| **axum** | Rust | `git clone --depth=1 https://github.com/tokio-rs/axum.git repos/axum` |

## Pinning

Ground-truth files are only valid for the exact commit they were generated against. Commits are tracked in `PINNED_COMMITS.json` (format: `{"repo": {"commit": "sha", "language": "lang"}}`) — the runner warns if a repo is at a different commit.

After cloning, record the SHA:

```bash
cd repos/flask
SHA=$(git rev-parse HEAD)
echo "Pinned flask to $SHA"
```

Then update `PINNED_COMMITS.json`:

```json
"flask": { "commit": "<SHA>", "language": "python" }
```

To restore a pinned state:

```bash
cd repos/<name>
git checkout $(python3 -c "import json; print(json.load(open('../PINNED_COMMITS.json'))['<name>']['commit'])")
```

**When you update a pin**: regenerate ground-truth for that repo (`bash bench/gen-ground-truth.sh --repo <name>`) and update `PINNED_COMMITS.json`. Stale ground-truth produces misleading scores.

## Size Reference

| Repo | Language | Files | Lines (approx) |
|---|---|---|---|
| flask | Python | ~24 | ~9.5k |
| discourse | Ruby/Rails | ~21,500 | ~750k (Ruby + TS/JS) |
| openproject | Ruby/Rails | ~20,600 | ~670k (Ruby + TS/JS) |
| gin | Go | ~120 | ~15k |
| maket | Ruby/Rails | ~1,243 | ~207k |
| nextjs | TypeScript | ~8,000 | ~500k |
| javalin | Java | ~450 | ~35k |
| axum | Rust | ~300 | ~25k |
