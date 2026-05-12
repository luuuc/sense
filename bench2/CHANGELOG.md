# bench2 Changelog

## 2026-05-11 — Initial implementation + fixes

### Created

- `bench2/` directory — fully autonomous, zero dependencies on `bench/`
- `scenarios/{flask,gin,axum,discourse,javalin,nextjs}.yaml` — 6 scenarios, 4 steps each
- `lib/scenario.py` — YAML parsing, schema validation, prompt generation
- `lib/scorer.py` — transcript parsing, checklist matching, miss detection
- `lib/reporter.py` — per-scenario comparison tables + aggregate ranking
- `run.sh` — scenario runner (tool × repo → transcript.json)
- `score.sh` — batch scorer wrapper
- `report.sh` — report generator (terminal/markdown/json)
- `tools/` — symlinks to bench/tools/*.sh
- `README.md`, `description.md`, `CHANGELOG.md`

### Fixed: macOS bash 3.2 compatibility

- Replaced `declare -A` associative arrays with `scenario_repo()` helper function
- `declare -A` is bash 4+ only, macOS ships bash 3.2

### Fixed: broken symlinks

- Relative paths corrected from `../bench/` to `../../bench/tools/`
- Symlinks pointed to non-existent `bench2/bench/` directory

### Fixed: MCP isolation (port from bench/)

- Added `strip_user_hooks()`, `strip_user_mcp()` functions
- Added `restore_user_settings()` with `trap cleanup EXIT`
- `codebase-memory-mcp` gets its config restored per-iteration
- All other tools run with clean user-level config

### Fixed: `wall_time` reporting (scorer)

- `duration_ms` now read from top-level result event (`obj.duration_ms`)
- Previously looked in nested system init event which didn't have it
- Result: changed from `0.0s` to correct value (e.g., `209.2s`)

### Fixed: token reporting (scorer)

- Now reports `token_input_uncached`, `token_output`, `token_cache_read`, `token_cache_write` separately
- `token_total_billed` = uncached input + output (what you pay for)
- `token_total_all` = everything including cache
- Previously only reported a single `token_total` mixing cached + uncached

### Fixed: miss detection (scorer)

- Now distinguishes three categories:
  - **pre_mcp_misses** — grep/Glob/Read calls made BEFORE any MCP tool was used (real bypass)
  - **post_mcp_verification_reads** — Read calls after MCP to source files (supplemental, NOT penalised)
  - **post_mcp_misses** — grep/Glob calls after MCP (still penalised)
- `total` = `pre` + `post_mcp_misses` only (verification reads excluded)
- Previously counted ALL Read calls equally, penalising legitimate supplemental reading

### Fixed: added `response_richness` check type

- New check type measures output depth without ground truth — counts unique source files
  referenced by `file:line` or `file:symbol` patterns in the transcript
- Does not require ground truth, uses regex: `([\w/\-_.]+\.(ext))[:>](line|symbol)`
- Excludes non-source files (.md, .txt, .json, images, etc.)
- Added per-step richness to scored.json + Rich column in report tables
- Thresholds calibrated from real transcript data (actual measurements across 12 sessions):
  - flask: 1-2 files (small transcripts) → thresholds at 2
  - gin: 7 files both tools → thresholds at 3-5
  - axum: 15-17 files → thresholds at 4-8
  - discourse: 8 files both → thresholds at 4-5
  - javalin: 6 baseline vs 11 sense → threshold 7 in step2 creates DIFFERENTIATION
  - nextjs: 15 files → thresholds at 3-6
- Updated `scenario.py` validator to accept `response_richness`
- Updated all 6 scenarios with calibrated richness thresholds

- All 95 checks were `contains` type — any mention of a symbol name hit
- Added new check types: `word`, `starts_with`, `mcp_tool_used`, `no_grep`
- Rewrote all 6 scenarios with stricter, more discriminating checks
- Required items now verify actual structural findings, not just keyword presence
- Updated `scenario.py` validator to accept the new check types

### Fixed: scoring model (removed fluency/discoverability from composite)

- Fluency and discoverability penalised tools for *any* grep usage, fundamentally
  wrong — code intelligence tools are enablers, not grep replacements
- Those dimensions also structurally biased towards baseline (always 0 misses)
- New score: `0.6 × completeness + 0.4 × efficiency` only
- Grep, Read, and MCP counts are now supplementary data (reported, not penalised)
- Updated all 6 scenarios to `weights: {completeness: 0.6, efficiency: 0.4}`
- Updated `description.md` with the new scoring philosophy

### Fixed: reporter shows tool call counts instead of miss/fluency

- Per-scenario tables now show: Score, Completeness, Tokens, Grep, Read, MCP, Time, Cost
- Aggregate shows: Avg Score, Avg Comp, Avg Tokens, Avg Grep, Avg MCP, Total Cost
- Metric legend updated with grep (lower=better), read (lower=better), mcp (higher=better)
- Removed fluency and misses from report tables

### Known: 0-byte claude.log

- Same as bench/ — `claude --verbose --output-format stream-json` sends everything to stdout
- Stderr only gets fatal errors; exit code verification serves as health check
