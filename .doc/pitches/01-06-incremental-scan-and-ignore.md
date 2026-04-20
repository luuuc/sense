# 01-06 — Incremental scan & ignore rules

**Appetite:** 1–2 days (solo founder + AI)
**Status:** Shipped — pending PR

## Problem

After 01-05, `sense scan` re-parses every file every time. On a 500-file Ruby project that's ~10 seconds for no reason when only 3 files changed. Worse: Sense indexes **everything** inside the working tree — `vendor/`, `node_modules/`, generated SQL dumps, minified JS. The index bloats and the first scan takes minutes when it should take seconds.

Both problems have the same fix: **know what changed, and respect the ignore rules you already have**. The schema in 01-01 already has `sense_files.hash`; it's just never compared. The AI workflow doc (`08-ai-workflow.md`) explicitly assumes incremental scans keep the index fresh — that assumption is currently fiction.

## Appetite

1–2 real days. SHA-256 hash comparison + three ignore layers + a 4-key config loader. The math is trivial. The rabbit hole is file-change detection on deleted / renamed files — bound that carefully and we're done.

## Solution

```
sense scan
    │
    ▼
load .gitignore, .senseignore, config.yml ignore → composite matcher
    │
    ▼
walk working tree, filter via matcher
    │
    ▼
for each file:
    new_hash := sha256(file)
    if sense_files.hash == new_hash  → skip
    else                             → re-parse + replace symbols/edges
    │
    ▼
for each path in sense_files not seen during walk:
    → cascade delete (symbols + edges removed via FK ON DELETE CASCADE)
```

Three ignore layers, merged in order:

1. `.gitignore` — always honored (use `github.com/go-git/go-git/v5/plumbing/format/gitignore` or similar)
2. `.senseignore` — same syntax, project root, for things tracked by git but not worth indexing
3. `config.yml` `ignore:` list — same syntax, for user-level overrides

### Minimal `config.yml`

```yaml
ignore:
  - vendor/
  - node_modules/
scan:
  max_file_size_kb: 512
```

Only two keys in this pitch. Full config schema is a later concern. Missing file is fine — defaults apply.

### Deleted-file handling

A file present in `sense_files` but absent from the walk output is deleted or ignored. Cascade-delete its symbols and edges (FK from 01-01 already handles this on symbol rows; confirm edges cascade too). Do this in one transaction at the end of the scan so partial failures don't leave orphans.

## Rabbit holes

- **Hash granularity.** SHA-256 of the full file contents, not just the parse output. Cheap enough (~300MB/s with stdlib). Don't try to hash just the AST; it complicates rename detection and saves nothing.
- **Renames.** Git sees a rename; Sense sees a delete + create. For v1, that's fine — the symbols re-land with the new file_id. Historical graph ("this symbol was moved") is out of scope.
- **Binary and generated files.** Hash is fine, but don't try to parse — `scan.max_file_size_kb` (default 512) skips huge files. Extension-based language detection from 01-02 won't even match binaries. Belt-and-braces.
- **Symlinks.** `filepath.Walk` follows them by default; that leads to infinite loops when a project has a `node_modules/.bin/` symlink to `..`. Use `fs.WalkDir` + explicitly don't follow symlinks outside the root.
- **`.gitignore` in submodules / nested repos.** Respect nested `.gitignore` files, not just the root one. The `go-git` library handles this; roll-your-own doesn't.

## No-gos

- **Watch mode** — 04-01. This pitch is invocation-time incremental only.
- **Full config schema** — embeddings/languages/batch_size blocks per `04-storage.md`. Two keys now, grow later.
- **Parallel re-parse** — nice in theory, premature now. Sequential per-file is fine at the scale Sense targets.
- **Change detection via filesystem events** — that's what watch mode is for.
- **Schema migrations on the index** — `04-storage.md` promises "no migration, just rebuild." Honor it: if a scan detects schema version mismatch, drop the DB and start fresh.

## Acceptance criterion

This pitch ships when (a) a second `sense scan` on an unchanged Sense repo completes in under 500 ms, (b) deleting a tracked file and re-scanning leaves zero orphan symbols and zero orphan edges in the index, and (c) adding `vendor/` to `.senseignore` excludes it from the next scan.

## Scope

- [x] **Ignore matcher** — `internal/ignore/matcher.go` composing `.gitignore` + `.senseignore` + config `ignore:` into one predicate
- [x] **Nested `.gitignore` handling** — respect per-directory `.gitignore` as git does
- [x] **Config loader** — `internal/config/config.go` reading `.sense/config.yml`, two keys (`ignore`, `scan.max_file_size_kb`), YAML via `gopkg.in/yaml.v3`
- [x] **SHA-256 incremental** — compare against `sense_files.hash`, skip unchanged files, update hash + `indexed_at` for changed files
- [x] **Deleted-file cascade** — any path in `sense_files` not seen this walk is removed; FK cascades clear symbols + edges in the same transaction
- [x] **Deleted-file cascade test** — `TestScan_DeletedFileCascade` creates a fixture, scans, deletes a file, re-scans, asserts no orphan rows in `sense_symbols` or `sense_edges` pointing at the deleted file
- [x] **Symlink safety** — `fs.WalkDir` without following symlinks, skip `.sense/` itself
- [x] **Size cap** — skip files above `scan.max_file_size_kb`, log at info level
- [x] **Scan reporting** — on completion, print `scanned N files (K changed, M skipped) in Xms`
- [x] **Environment variable overrides** — `SENSE_DIR`, `SENSE_MAX_FILE_SIZE`, `SENSE_EMBEDDINGS_ENABLED` plumbing (the last becomes load-bearing in Cycle 2)
- [x] **Benchmark** — second `sense scan` on the Sense repo with no changes completes in <500ms
