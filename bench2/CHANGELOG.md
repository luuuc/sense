# bench2 Changelog

## 2026-05-12 — Fair scoring overhaul

### Two-layer scoring model

The old 4-dimension score (`completeness × efficiency × tool_fluency × discoverability`) conflated answer quality with tool adoption — 35% of the score came from dimensions baseline could never win. The fairness gap was +0.159 but transcript analysis showed the real gap should be ~0-0.08.

New model:
- **Fairness score** = 0.70 × correctness + 0.30 × efficiency (for Sense vs Baseline)
- **Adoption score** = 0.60 × tool_fluency + 0.40 × discoverability (for code-intel comparisons only)

Checks tagged `layer: adoption` are excluded from the fairness score.

### Scenario check cleanup

- Tagged all 23 `mcp_tool_used`/`no_grep` checks with `layer: adoption`
- Promoted to required: `FlaskClient` (flask), `CurrentUserSerializer` (discourse), `NEXT_REQUEST_ID_HEADER` (nextjs)
- Added checks: `BasicAuthForRealm` (gin), `spam_rules_spec`/`reply_as_new_topic`/`queue` (discourse), `AppContext` (flask)
- Fixed stale checks: `conftest.py` → `test_reqctx`, `BaseServer` → `base-server`, deduplicated nextjs checks, removed `can_edit_tags`, lowered gin/axum `response_richness` thresholds

### Per-repo efficiency calibration

Replaced flat 8k-60k token range with per-repo ceilings: Flask/Gin/Javalin 15k, Axum 20k, Discourse 30k, Next.js 40k.

### Improvement loop overhaul

- `SKILL.md` renamed to `LOOP-CONTEXT.md` — rewritten for fairness scoring
- 3-loop structure (Verifiability/Semantic Depth/Weight Optimization) collapsed to single loop with iterations
- Phase instructions rewritten: optimize for fairness gap accuracy, not Sense advantage
- `improve-loop.sh` calls `claude -p` as transcript reviewer (default Opus 4.7) instead of pausing for human
- Added `--reviewer-model` and `--iterations` flags

### Doc cleanup

- Deleted 7 stale files from `improvement-loop/docs/`
- Rewrote `description.md` and `README.md` for two-layer scoring

### Results

Fairness gap: Sense 0.823 vs Baseline 0.805 (+0.018). Baseline wins on Javalin and Axum.

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
