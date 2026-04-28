# Benchmark Repos

Clone these repos locally before running the harness. Pin to exact commits for reproducible ground-truth.

## Repos

| Repo | Language | Clone | Commit |
|---|---|---|---|
| **flask** | Python | `git clone https://github.com/pallets/flask.git repos/flask` | Pin on first clone |
| **discourse** | Ruby/Rails | `git clone https://github.com/discourse/discourse.git repos/discourse` | Pin on first clone |
| **openproject** | Ruby/Rails | `git clone https://github.com/opf/openproject.git repos/openproject` | Pin on first clone |
| **gin** | Go | `git clone https://github.com/gin-gonic/gin.git repos/gin` | Pin on first clone |
| **nextjs** | TypeScript | `git clone https://github.com/vercel/next.js.git repos/nextjs` | Pin on first clone |

## Pinning

Ground-truth files are only valid for the exact commit they were generated against. Commits are tracked in `PINNED_COMMITS.json` — the runner warns if a repo is at a different commit.

After cloning, record the SHA and update the pinned commits file:

```bash
cd repos/discourse
SHA=$(git rev-parse HEAD)
echo "Pinned discourse to $SHA"

# Update PINNED_COMMITS.json with the SHA
# Then generate ground-truth for that commit
```

To restore a pinned state:

```bash
cd repos/<name>
git checkout $(python3 -c "import json; print(json.load(open('../PINNED_COMMITS.json'))['<name>'])")
```

**When you update a pin**: regenerate ground-truth for that repo and update `PINNED_COMMITS.json`. Stale ground-truth produces misleading scores.

## Size Reference

| Repo | Files | Primary Language |
|---|---|---|
| flask | ~24 | Python (~9.5k lines) |
| discourse | ~21,500 | Ruby (~427k lines), TS/JS (~320k lines) |
| openproject | ~20,600 | Ruby (~509k lines), TS/JS (~164k lines) |
| gin | ~120 | Go |
| nextjs | ~8,000 | TypeScript |
