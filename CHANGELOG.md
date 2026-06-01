# Changelog

All notable changes to Sense.

## [Unreleased]

### Breaking Changes

- Dead-code analysis now emits honest verdicts. The `sense_graph dead_code=true`
  MCP response and `sense dead --json` output replace the flat `dead_symbols`
  array with `unreferenced_symbols`, split into an earned `dead` list (provably
  safe to remove, each with a per-symbol `verify` grep) and `possibly_dead`
  groups (a hidden caller could exist, grouped by reason, each with a `verify`
  recipe). The two-value `confidence` field is removed. A symbol earns `dead`
  only when a language voice can prove closed-world for its language; only Ruby
  ships a voice, so all other stacks report `possibly_dead`. This makes a
  confident-but-wrong `dead` impossible on unsupported stacks.

### Fixed

- Blast radius no longer fans out across temporal co-change hops. A temporal
  (git co-change) edge was traversed transitively, so a shared co-changed test
  file could bridge two unrelated models and then pull in the whole hub's
  `references` callers — e.g. changing `Shipping::Rate` falsely implicated
  `Country`'s address-form and geolocation callers. A temporal edge is now a
  sink: a node reached *only* via temporal coupling is still reported (it stays
  a co-change caller and still raises risk) but is not expanded, so co-change
  cannot launder into a transitive structural path. Nodes reached by any real
  (`calls`/`composes`/`tests`/…) edge are unaffected. On a real Rails app this
  cut the false-positive radius of a hub-adjacent model by ~40% while retaining
  every genuine caller.
- Dead-code `dead` precision on real-world Ruby. The closed-world proof assumed
  the resolver binds every call, which is false on a dynamic language (inherited
  bare calls, `**splat` args, chain receivers, `validate :sym` symbol arguments
  all go unbound), so live methods were falsely reported `dead` (0.22 precision
  on a real Rails app). A soundness gate now withholds `dead` unless the symbol's
  name is absent from a project-wide *mention set* harvested at scan time —
  mentioned nowhere it could be an unresolved caller. A live-but-unbindable call
  still leaves a textual mention, so the symbol stays `possibly_dead` (new reason
  `core_name_mentioned`) instead of becoming a false `dead`. This raised
  precision to 1.00 on the same app while preserving the genuinely-dead findings.
  The gate fails closed: if the mention harvest is unavailable (a pre-feature
  index), no symbol earns `dead` until the next full rescan.
- `validate :method` custom-validation callbacks now emit a `calls` edge to the
  named predicate (added to the Rails callback set), so validation methods
  resolve to their framework caller in `sense_graph`/`sense_blast` instead of
  reading as unreferenced.

## [0.43.0] - 2026-05-01

### Features

- print instant banner on scan start
- add live progress display with TTY detection
- replace per-file warning prints with grouped collection
- add warning hint line to scan summary
## [0.42.0] - 2026-05-01

### Features

- add maket as medium-sized benchmark repo
- add pair tokenization for cross-encoder scoring
- add ONNXReranker cross-encoder implementation
- add cross-encoder reranking to search pipeline
- bundle cross-encoder reranker in binary
## [0.41.0] - 2026-05-01

### Bug Fixes

- restore .claude dir correctly when regenerated during run

### Features

- add naming-convention edge pass for Rails/Django projects
- segment graph callers into production vs test
- add production/test counts to blast response
## [0.40.0] - 2026-05-01

### Bug Fixes

- correct scorer normalization for Symbol:filepath format and add class-level partial credit
- add word boundary to go.mod framework patterns
- anchor go.mod regex patterns to line start

### Features

- enrich descriptions with instance type names
- rank summary by type diversity instead of strength
- add framework detection and hub-type role hints
- exclude interface methods and framework hooks from results
## [0.39.0] - 2026-05-01

### Features

- suppress all hooks during benchmark via SENSE_BENCH env var
- isolate benchmark tools from ambient Sense config
- chunked writes, model migration, configurable dims, and file path context
- improve ranking with weight floor, multi-query expansion, and graph enrichment
## [0.38.0] - 2026-04-28

### Bug Fixes

- tune large-tier defaults from benchmark validation

### Features

- add auto-calibration profile computation and storage
- wire profile defaults into blast, conventions, and search handlers
- surface calibration profile in status output
## [0.37.0] - 2026-04-28

### Features

- add multi-edge BFS with confidence decay and result cap
- expose grouped blast results in CLI and MCP
## [0.36.0] - 2026-04-28

### Features

- add CamelCase/snake_case decomposition for FTS5
- add confidence-gated fusion weights
## [0.35.0] - 2026-04-28

### Bug Fixes

- address council review polish items

### Features

- add structural orientation to sense_status response
- prepend one-sentence summary to conventions response
## [0.34.0] - 2026-04-28

### Features

- cap output with instance sampling, token budget, and stricter defaults
## [0.33.0] - 2026-04-28

### Bug Fixes

- include fwcd tree-sitter grammars in Dependabot group
- use portable timeout fallback for macOS
- add --verbose flag required by stream-json output
- remove --bare flag that strips authentication
- advise instead of deny for non-explorer agent spawns
- backfill embeddings when no files changed
- auto-resolve dominant match to prevent LLM retry spirals

### Features

- add task definitions and ground truth for competitive evaluation
- add tool setup scripts with verified MCP commands
- add scorer, reporter, and task parser
- add runner, score, and report entry points
- populate verified ground-truth for gin and nextjs
- add --write-config mode to tool scripts
- improve scorer with Go package normalization and cache tokens
- restructure runner with persistent workspaces
- add suffix and containment resolution tiers with structured MCP responses

### Refactoring

- replace sense self-evaluation with flask as fifth benchmark repo
## [0.31.0] - 2026-04-26

### Features

- add benchmark runner and CLI command
## [0.30.0] - 2026-04-26

### Features

- add embedding debt tracking methods
- split scan into structural and embedding phases
- add search mode reporting and concurrent-safe vector swap
- background embedding with watch mode integration
- add PostToolUse handler for auto-index update
- add tool detection framework
- add Cursor and Codex CLI integration writers
- add multi-tool `sense setup` command
- add NextStep type and response field plumbing
- add next-step hint logic to all MCP handlers
- add dead code detection query engine
- add dead code response types and builder
- add dead_code parameter to sense_graph
- add temporal edge kind and response types
- add temporal coupling extraction from git history
- expand BFS frontier through temporal edges
- wire temporal edges into graph and blast responses

### Refactoring

- use setup.Options for first-run, remove --init flag
## [0.24.5] - 2026-04-26

### Features

- add `sense update` command

### Refactoring

- remove automatic version checking
- remove spatial TUI and all Charm dependencies
## [0.24.4] - 2026-04-25

### Features

- close Sense bypass escape hatches in PreToolUse
- reframe Sense from "structural" to "codebase understanding"
## [0.24.3] - 2026-04-25

### Features

- enforce Sense tool usage via PreToolUse deny responses
- strengthen session-start and subagent-start guidance
- upgrade CLAUDE.md template, matcher, and MCP instructions
## [0.24.2] - 2026-04-25

### Bug Fixes

- skip network call on first run to avoid macOS firewall prompt
## [0.24.1] - 2026-04-25

### Bug Fixes

- ad-hoc codesign binary on macOS to prevent "modified" warning
## [0.24.0] - 2026-04-25

### Features

- add table-driven generic extractor
- add specs and fixtures for 7 languages
- wire langspec into registry and tier map
## [0.23.5] - 2026-04-24

### Features

- add AI tool config generator
- add Claude Code lifecycle hooks
- run setup on first scan and add --init flag
## [0.23.2] - 2026-04-24

### Features

- add darwin/amd64 (Mac Intel) support
## [0.23.0] - 2026-04-23

### Features

- replace FormatInput with graph-aware FormatContext
- add ContextForFile for graph-derived embedding context
- wire context builder and method body extension into embed phase
- add snippet to FTS5 index for keyword code search

### Performance

- tune HNSW EfSearch=200 and expand path demotions
## [0.22.9] - 2026-04-23

### Performance

- configure ONNX session thread affinity
- add PRAGMAs, bulk hash query, and prepared statements
- parallel parse, batched writes, and per-phase timing
## [0.22.8] - 2026-04-23

### Bug Fixes

- harden HNSW incremental updates against coder/hnsw corruption

### Features

- demote db/migrate and script symbols via path-based weighting
- extract AMS serializer composition as composes edges
- resolve bare receiverless method calls in Ruby
## [0.22.7] - 2026-04-23

### Features

- aggregate callers across class reopenings via multi-seed BFS
- demote module symbols and fix pipeline ordering
## [0.22.6] - 2026-04-22

### Features

- add incremental HNSW index updates
- wire incremental HNSW updates into scan pipeline

### Performance

- skip Fruchterman-Reingold layout during scan
## [0.22.5] - 2026-04-22

### Features

- add --cpuprofile and --memprofile flags for pprof profiling
## [0.22.4] - 2026-04-22

### Bug Fixes

- suppress version notice in --json mode
- walk inherits edges in reverse during BFS traversal
- replace per-edge warnings with edge summary line

### Features

- add --file and --language flags for symbol disambiguation
- add HNSW index persistence and parallel ONNX embedding
- add cross-directory test association for Rails mirror trees
## [0.22.3] - 2026-04-22

### Features

- add session status bar with live metrics and token formatting
- add pulse indicator with sine-wave breathe cycle and event flash
- add nudge engine with trigger table and milestone celebrations
- add ecosystem prompts with persistence and x-key dismiss
- wire dynamic status layer into TUI and add sense status --live
## [0.22.2] - 2026-04-21

### Features

- strengthen server instructions to prefer Sense over grep/glob
## [0.22.1] - 2026-04-21

### Features

- add symbol selection with directional navigation and fuzzy-find
- add blast radius animation with hop-distance color rings
- add semantic search with debounced query and score-based illumination
- wire cross-mode transitions with state cleanup guarantees

### Refactoring

- move EmbeddingsEnabled logic from cli to config
## [0.22.0] - 2026-04-21

### Features

- add braille canvas rendering primitive
- add force-directed layout engine with caching
- add graph renderer with viewport, zoom, and labels
- add color lens system with dark/light palette detection
- wire Bubble Tea program with animation and status bar
## [0.21.4] - 2026-04-21

### Features

- auto-add .sense/ to .gitignore on first scan
## [0.21.3] - 2026-04-21

### Features

- add server instructions and rewrite tool descriptions for LLM discoverability
## [0.21.2] - 2026-04-21

### Bug Fixes

- lower min_strength default from 0.5 to 0.0
- add default ignore patterns for vendor trees and minified bundles
- cap ambiguous warnings at 20 and add --quiet flag
- normalize RRF scores to 0-1 with min-max scaling
- traverse composes and includes edges in BFS
## [0.21.1] - 2026-04-21

### Bug Fixes

- surface composes, includes, and imports edges in graph response
- align search min_score default with CLI (0.5 → 0.0)
- disable MCP stdio server in watch mode
## [0.21.0] - 2026-04-21

### Features

- add non-blocking version check with 24h cache
- add VERSION pinning, GITHUB_TOKEN auth, and Gatekeeper fix
## [0.20.0] - 2026-04-21

### Features

- add Rust depth extraction — visibility, derives, trait resolution, field composition
## [0.19.0] - 2026-04-21

### Features

- add Go depth extraction — visibility, embeddings, receiver resolution, interface methods
## [0.18.0] - 2026-04-21

### Features

- add Django, FastAPI, and dataclass framework edges to Python extractor
## [0.17.0] - 2026-04-21

### Features

- add React JSX edges, default export naming, types, dynamic imports, and re-exports
## [0.16.0] - 2026-04-20

### Features

- add RawExtractor interface and Stimulus naming helpers
- add ERB extractor for Stimulus, Turbo, and Turbo Frames
- add Stimulus controller inference to TS/JS extractor
- add Turbo broadcasts and importmap resolution to Ruby extractor
- integrate RawExtractor and add cross-language integration tests
## [0.15.0] - 2026-04-20

### Bug Fixes

- handle nullable source_id in edge resolution and queries

### Features

- make source_id nullable for file-level edges (schema v3)
- add Rails-aware extraction to Ruby extractor
## [0.14.0] - 2026-04-20

### Features

- detect schema version mismatch and auto-rebuild index
- track embedding model version in sense_meta
- read actual version metadata in status, doctor, and --version
## [0.13.0] - 2026-04-20

### Features

- add per-tool savings estimation formulas
- add session and lifetime counter tracking
- show session and lifetime savings in `sense status`
## [0.12.0] - 2026-04-20

### Features

- enhance `sense status` with language breakdown, coverage, and --json
- add `sense doctor` diagnostic command

### Refactoring

- export language tier map as single source of truth
## [0.11.0] - 2026-04-20

### Features

- add fsnotify watcher with recursive directory registration
- add incremental re-index for targeted file sets
- add watch mode orchestrator with concurrent MCP serving
- add `sense scan --watch` flag with integrated MCP
## [0.10.0] - 2026-04-20

### Features

- add convention detection engine
- add ConventionsResponse types and MarshalConventions
- add `sense conventions` command
- add sense_conventions tool to MCP server
## [0.9.0] - 2026-04-20

### Features

- add FTS5 virtual table and keyword search queries
- add HNSW vector index, query embedding, and RRF fusion engine
- add SearchResponse types and MarshalSearch
- add `sense search` command with hybrid search
- add sense_search tool to MCP server
## [0.8.0] - 2026-04-20

### Features

- add ONNX embedding pipeline with bundled model
- add pass 3 embedding generation for changed symbols
- add status command and embeddings config escape hatch
## [0.6.0] - 2026-04-20

### Features

- add gitignore-compatible path matcher with nested support
- add YAML config loader for .sense/config.yml
- add FileMeta, FilePaths, and DeleteFile for incremental scan
- add incremental scan with ignore rules and size cap
## [0.5.0] - 2026-04-20

### Features

- add nullable metrics, Freshness type, and StatusResponse
- check context cancellation between BFS hops
- add stdio MCP server with graph, blast, and status tools

### Refactoring

- export helpers for MCP server reuse
## [0.4.0] - 2026-04-19

### Features

- expose DB() accessor for read-path consumers
- add shared MCP-schema marshalling layer
- add sense graph and sense blast with three-tier lookup
- wire graph and blast into the main dispatcher
## [0.3.0] - 2026-04-19

### Features

- add SymbolRef + centralize nullable-symbol hydration
- add WalkNamedDescendants helper + confidence constants
- emit calls edges from function bodies
- emit calls edges + literal send dispatch
- emit calls edges + literal getattr dispatch
- emit calls edges with this-prefix strip
- emit calls edges for fn + impl bodies
- introduce scope-aware qualified-name resolver
- two-phase resolution with unresolved diagnostics and test association
- add BFS engine with risk classifier

### Performance

- make idx_sense_edges_target covering for BFS
## [0.2.0] - 2026-04-18

### Features

- bundle tree-sitter grammars for 6 languages
- define Extractor interface and registry
- add fixture test harness with -update flag
- add Tier-Basic Ruby extractor
- add Tier-Basic Python extractor
- add Tier-Basic TS/TSX/JS extractor
- add Tier-Basic Go extractor
- add Tier-Basic Rust extractor
- index symbols and edges with transactional writes
## [0.1.0] - 2026-04-17

### Features

- add row-level types for the index schema
- define the storage contract
- add Index conformance suite
- add pure-Go SQLite adapter
- add working-tree walker
- wire sense scan, help, and version
