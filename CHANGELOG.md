# Changelog

All notable changes to Sense.
## [1.11.7] - 2026-07-03

### Bug Fixes

- split sense_graph inherits into directed inherits/inherited_by
## [1.11.6] - 2026-07-02

### Bug Fixes

- exclude bare-name collision edges from hub symbols
- ignore Next.js _next build output by default
- rate unknown-receiver calls by receiver knownness
## [1.11.5] - 2026-07-01

### Bug Fixes

- resolve celery task dispatch to the task function
## [1.11.4] - 2026-06-29

### Bug Fixes

- surface reverse-composition dependents for ORM models
## [1.11.3] - 2026-06-25

### Bug Fixes

- steer agents to cite from Sense refs instead of re-reading files
## [1.11.2] - 2026-06-25

### Bug Fixes

- nudge toward Sense on cd-prefixed and quoted greps
## [1.11.1] - 2026-06-23

### Bug Fixes

- de-noise and re-rank sense_conventions output
## [1.11.0] - 2026-06-22

### Features

- collapse already-seen callers in sense_blast (graph/blast session dedup)
## [1.10.0] - 2026-06-21

### Features

- right-size sense_blast output with area-stratified enumeration
## [1.9.0] - 2026-06-19

### Features

- add completeness verdict and per-result relation to sense_blast/sense_graph
## [1.8.0] - 2026-06-18

### Features

- qualify colliding bases, exclude test scaffolding
## [1.7.1] - 2026-06-17

### Bug Fixes

- guard against nil source in acts_as mixin expansion
## [1.7.0] - 2026-06-17

### Bug Fixes

- deterministic caller cap on high-fan-out symbols

### Features

- add file param to disambiguate multi-match symbols
- resolve acts_as_* mixin dependents to collaborator classes
## [1.6.0] - 2026-06-15

### Features

- resolve relatively-named superclasses via lexical scope
## [1.5.0] - 2026-06-15

### Features

- resolve class_name_attribute config-string edges
## [1.4.0] - 2026-06-15

### Features

- emit enqueue edges from Sidekiq/ActiveJob calls
- emit super-call edges to inherited methods
- resolve inherited method dispatch via the class chain
## [1.3.0] - 2026-06-15

### Bug Fixes

- pre-load Sense tools, retire ToolSearch adoption gate
- credit all location-pin forms in cited-recall scorer

### Features

- lead report with split mention/cited/billed axes

### Refactoring

- rework scenarios to grep-hostile relational seams
## [1.2.0] - 2026-06-14

### Features

- register Sense MCP with Codex via .codex/config.toml
- write OpenCode adoption plugin steering models to Sense tools
- add Codex and OpenCode/Ollama harness runners
- score gold-target recall alongside fairness
- add model-sweep, variance, and session runners
- add Rails-vertical scenarios and pin their commits
## [1.1.0] - 2026-06-10

### Features

- add code-intel MCP benchmark leaderboard page
## [1.0.1] - 2026-06-07

### Bug Fixes

- report live heap for query serving, not alloc churn
## [1.0.0] - 2026-06-06

### Features

- Sense 1.0, first stable release
## [0.99.2] - 2026-06-06

### Refactoring

- drive tool integration from a registry
- drive DetectCurrent from the registry
## [0.99.1] - 2026-06-06

### Bug Fixes

- install git-cliff on PATH so auto-release actually tags
## [0.99.0] - 2026-06-05

### Bug Fixes

- make example ordering deterministic
- disambiguate repeated representative labels

### Refactoring

- inject the embedder behind an unexported factory seam
- extract extractLib from ensureORTLib for testability
- put the index behind an unexported indexStore seam
- split server.go into concern files (pure move)
- decompose buildMCPServer and add io.Writer diagnostics seam
- decompose handleGraph, handleSearch, resolveDispatchCallers
- decompose buildStatusResponse into index-count and version helpers
- rename graph lookup closure local to avoid shadowing params
- extract collector sink + harvested-name partition
- move parse + util helpers out of scan.go
- decompose walkTree into four phase helpers
- decompose Run into phase helpers
- decompose the eight smaller scan-package functions
- split adapter.go into reads/writes/graph/embeddings; decompose ReadSymbolGraph
- split search.go into fusion/ranking/enrich/fallback; decompose Search
- decompose Compute into a bfsState BFS over named hop steps
- split detectors into per-family files and decompose
- split the dead-code package into cohesive files
- split test-DSL and symbols into tests.go/symbols.go
- split call resolution and type inference into calls.go/typeinfer.go
- split constant/DSL machinery into constants.go
- extract Rails/route DSL edge emitters into rails.go
- split extractor by concern, cover handlers, decompose walk
- split extractor by concern, cover types, decompose impl/derive/compose
- decompose importTarget into grammar-agnostic strategies
- extract type inference into typeinfer.go
- split framework.go by Django/annotation/FastAPI concern
- decompose Resolve and isTestPath to clear the ledger
- extract a testable run() seam over main
- inject builder process edges behind a deps seam

### Style

- goimports -local regroup + gofmt, no behavior change
## [0.98.0] - 2026-06-03

### Bug Fixes

- repoint dead --force advice at sense scan --rebuild
- refresh pending count when background embed completes

### Features

- unify index reset into one metrics-preserving primitive
- add sense scan --rebuild flag
- track last re-index time and embedding debt in WatchState
- add watch toggle for the embedded watcher
- add background freshening service with single-writer lock
- host embedded watcher and read-repair stale files on query
- report watching and pending embeddings in sense status

### Refactoring

- replace PostToolUse re-index with session-start reconcile
- extract envOrConfigBool to share toggle fallback
## [0.97.0] - 2026-06-02

### Bug Fixes

- treat framework apps as non-library so internal methods aren't mislabeled
- gate unqualified fallback by language and demote cross-namespace guesses
- surface view templates as the source of inbound view edges
- demote unverified cross-scope guesses; seed diff-blast by changed line range
- gate the exact byQualified path; never bind production code to test symbols
- treat temporal co-change edges as a sink, not a bridge

### Features

- capture Ruby method visibility and reflective dispatch names
- replace dead-code cascade with open/closed-world arbiter
- harvest mentioned-name set into sense_meta
- gate the dead verdict on the mention set
- emit calls edge for validate :method callbacks
- add min_confidence to sense_graph; surface hidden callers
- per-language soundness gate for the dead verdict
- harvest Go mentions, reflection, and cgo exports
- Go language voice earns the dead verdict
- harvest Rust mentions, attributes, and trait impls
- persist Rust harvest sets to sense_meta
- Rust language voice earns the dead verdict
- TypeScript/JavaScript export visibility and dead-code harvest
- persist the TS/JS decorator and default-export harvest
- TypeScript earns the dead verdict; JavaScript stays conservative
- Python underscore visibility and dead-code harvest
- persist the Python visibility and harvest sets
- Python earns the dead verdict via pythonVoice
- langspec visibility, annotation, and mention harvest
- persist langspec annotated-name set
- langspec voice earns dead for Java, reasons for six

### Refactoring

- extract warnMetaWrite for graceful meta-write degradation
- extract shared HarvestMentions mention walker

### Style

- gofmt struct-field alignment
## [0.96.0] - 2026-05-30

### Bug Fixes

- detect view reach by edge file_id, not caller symbol
- drop framework-accessor and core-reflection calls

### Features

- honest view_edges signal, stop calling Hotwire dispatch a blind spot
- expose ExtractEmbeddedCalls for cross-package fragment parsing
- parse embedded Ruby in tags with the Ruby grammar
- resolve render-collection and form-model edges
- emit Rails route-helper symbols from the route DSL
- retarget *_path/*_url view references to route: symbols
- promote ERB from Basic to Full tier

### Style

- positive validity check in isRouteHelperName (staticcheck QF1001)
## [0.95.2] - 2026-05-30

### Bug Fixes

- filter synthetic ruby-core base symbols from results

### Features

- attribute nested Struct/Data/Class.new value objects as classes
- recognize value objects, retain framework-gated predicate softening
- scope dead-code verify_cmd to call sites with too-common flag
## [0.95.1] - 2026-05-30

### Bug Fixes

- exact flat vector index, sorted fusion, drop HNSW + reranker

### Features

- query-shape-aware fusion + generic-token penalty + mode hatch
## [0.94.0] - 2026-05-29

### Bug Fixes

- report honest result provenance instead of hardcoded "structural"
- populate tests_affected_count in diff blast response

### Features

- bound graph and blast responses to a token budget
- demote test symbols after normalization so impl ranks first
- stop flagging Rails/Hotwire framework entry points as dead
## [0.90.4] - 2026-05-29

### Bug Fixes

- aggregate class callees and floor low-confidence edges
- tier Ruby predicate methods as possibly-dead, add verify grep
- persist lifetime metrics write-through on each query

### Features

- resolve calls on rescue-bound exception variables
## [0.90.0] - 2026-05-29

### Bug Fixes

- tier Ruby service and result-object methods as possibly-dead

### Features

- sharpen Ruby/Rails graph and ERB indexing
## [0.89.0] - 2026-05-29

### Bug Fixes

- surface inheritors in sense_graph for trait/base symbols
- record Ruby constant and class references as edges
- close noise-filter gap and lift Ruby reference coverage
- filter ambiguous edges from blast, fold member callers, gate Ruby dead-code tiering

### Features

- plumb docstring field from extractor to sqlite
- emit godoc as docstring
- emit RDoc as docstring
- emit JSDoc as docstring
- emit PEP 257 docstring
- emit /// and /** */ as docstring

### Refactoring

- tighten docstring extractors to hit coverage floor
## [0.88.1] - 2026-05-22

### Features

- add SKILL.md for Smithery listing
- emit compact JSON and prune out-of-scope edge buckets
- alias hallucinated argument keys to canonical schema
- annotate graph/blast/dead with language-specific index caveats
- dockerize the harness with per-tool images
- judge CLI fallback, path-resolved grounding, serena onboarding overhead
## [0.84.3] - 2026-05-15

### Bug Fixes

- resolve outdir to absolute in build-mcpb.sh
## [0.84.2] - 2026-05-15

### Bug Fixes

- correct scoring engine biases and aggregation
- four bugs surfaced by the 2-iter e2e run

### Features

- track last_scan_at and show health verdict
- fold wall-time into fairness and score failed runs
- average across runs and guard fairness layer
- record iteration history and continue past rollbacks
- record model in run_meta.json
- add citation extraction and grounding library
- emit citation_grounding from scorer
- surface citation grounding in reports
- rephrase scenario step prompts in AI-agent voice
- add per-scenario rubrics for LLM judge
- add LLM-as-judge harness with prompt v1
- combine LLM quality + citation grounding into fairness
- add improvement-loop audit harnesses
- wire Phase 4 audit into improve-loop
- convergence-aware improvement loop with held-out anchor
- per-repo MAX_BUDGET_USD tiers + honest cost defaults
- build .mcpb bundles per platform

### Refactoring

- consolidate harness — promote bench2/ to bench/
## [0.84.1] - 2026-05-12

### Bug Fixes

- relax second-scan timing threshold for CI
## [0.84.0] - 2026-05-12

### Features

- add affected_symbols, affected_files, and graph_edges_traversed to blast response
- add EdgeReferences edge kind
- emit references edges for constants and variables
- include constants in dead code detection and blast radius
- add call-site context snippets to graph and blast responses
- add index freshness check and drop sense_status from session start
- add also_called_by enrichment for small repos
- add quick orientation section with entry points, hubs, and test structure
- tag adoption checks and calibrate scenarios

### Refactoring

- introduce two-layer fairness/adoption scoring model
- simplify improvement loop to single-loop iteration model
## [0.78.0] - 2026-05-12

### Features

- add verification hints and ref field to graph/blast responses
- replace deny/advise with nudge responses for non-blocking UX
- inject summary.md into SessionStart output
- generate deep-explore subagent via sense setup
- upgrade SubagentStart with tool-loading instructions

### Refactoring

- shorten CLAUDE.md Sense section
## [0.77.0] - 2026-05-10

### Features

- add structured project description extraction
- add false-positive filters for dunder, library API, and trait impl methods
- add parent-class rollup to blast output
- add Seen field to SearchResultEntry for session dedup
- implement response compaction for pitch 22-05
- add coverage_note to list-type responses
- add source snippets and external dependency detection
- add substring fallback and path boosting for hybrid search

### Refactoring

- extract formatScanAge pure function
## [0.65.0] - 2026-05-10

### Bug Fixes

- improve singularize edge cases and test coverage
- exclude type methods from blast radius output

### Features

- extract calls from RSpec test blocks with synthetic scopes
- resolve instance variable receivers via initialize type inference
- add multi-hop chain resolution via return-type map
- infer block parameter types from collection methods
- add variable-based dynamic dispatch heuristic
- add line numbers to blast and graph responses
- add scope extraction, dedup callback symbols, qualify scope edges

### Refactoring

- extract shared Rails callback names to model package
- decompose detectFrameworkIdioms into focused detectors
## [0.62.19] - 2026-05-08

### Bug Fixes

- eliminate embedController race and simplify to function fields
- drain background embed goroutine before closing resources
- handle --help flag correctly in setup command

### Features

- add Opencode as a first-class AI tool target

### Refactoring

- extract processBatch and embedController from Run()
- extract buildMCPServer for testability
- remove unnecessary init() function
## [0.61.0] - 2026-05-07

### Bug Fixes

- use underscore-separated tool names

### Features

- add ConfidenceUnresolved constant for low-confidence fallback edges
- improve receiver resolution for Ruby call edges
## [0.59.0] - 2026-05-06

### Bug Fixes

- partial matching for dotted qualifiers and module paths
- per-repo GT generators replace generic heuristics
- include structure and naming conventions in MCP response
- expand test-path exclusion to 12 SQL patterns
- sharpen CLAUDE.md prompt for evaluation runs
- unify test-file detection on IsTestPath
- close langspec convention detection gaps
- expand type members into BFS seed set for class/type kinds

### Features

- add Project section extracted from README first paragraph

### Refactoring

- gen-ground-truth.sh dispatches to per-repo generators
- collapse Record/RecordWithFallback into single method
- collapse N+1 rg processes into 2, add --max-filesize
- extract shared text fallback integration, add gating and dedup

### Wip

- ripgrep text fallback (scope card 1)
- response labeling and metrics (scope card 2)
- FTS5 identifier audit test (scope card 3)
- verification pass — 9/10 queries, multi-word ranking (scope card 4)
## [0.55.0] - 2026-05-05

### Bug Fixes

- neutralize user-level hooks and MCP during benchmark runs

### Features

- add TopSymbolsByReach and TopCallers queries
- add KeySymbolEntry to conventions and status wire types
- wire key symbols into conventions and status handlers

### Refactoring

- collapse tiered defaults into single DefaultParams()
- use Adapter.TopSymbolsByReach for key abstractions
## [0.54.0] - 2026-05-04

### Bug Fixes

- deduplicate Known Noise entries with overlapping patterns
- apply council review blockers
- resolve new linter warnings across codebase
- log corrupt frameworks meta instead of silently swallowing

### Features

- make summary.md step 1 in cold-start instructions
- write stub summary.md when no scan data exists
- restructure sections to Fingerprint, Main Areas, Key Abstractions, Reading Path, Known Noise
- add Next: tool hints to Main Areas, Key Abstractions, Reading Path
- filter utility hubs and noisy paths from Key Abstractions
- strip sense_metrics from MCP responses and cap next_steps
- per-edge-kind decay with lowered floor for structural edges
- resolve callers through interface dispatch

### Refactoring

- remove sense.orient tool
- extract interface dispatch queries from dead-code detector
- unexport scoring constants, document relative scores

### Wip

- search centrality + dead-code tuning (pitch 17-12)
## [0.50.0] - 2026-05-03

### Features

- rank representatives by edge count, lower interface threshold
- add key domain types detector and orient section
- add Go type alias and middleware factory detectors
## [0.47.4] - 2026-05-03

### Bug Fixes

- resolve latest version via redirect to avoid GitHub API rate limits
## [0.47.3] - 2026-05-03

### Bug Fixes

- filter NULL source_id in interface alive query
- stop blocking Grep/Bash fallbacks, pass Glob through

### Features

- add tier-aware orient budget and bump conventions caps
- default callers depth to 2
- add test file demotion for JS/TS, JVM, Python

### Refactoring

- extract shared token estimator, fail closed on error
## [0.47.0] - 2026-05-02

### Bug Fixes

- harden ground-truth generator against contamination
- normalize Ruby/Python namespaces and add bidirectional partial matching
- add setup timeout to scan.sh and run.sh

### Features

- add rescore-all.sh to batch-score all transcripts
- isolate benchmark repos outside project tree
- add rescan.sh for standalone cold-start scan timing
- consolidate setup.sh and rescan.sh into scan.sh
- add Direction type and multi-hop graph result types
- add multi-hop BFS traversal (depth 1-3)
- add relevance tier filtering and response shaping
- add sense.orient tool for codebase orientation
- promote parent symbols when children cluster in top-K
- add PascalCase class qualifier partial matching
- add design pattern, framework idiom, and architecture layer detection
- add interface awareness, test-ref exclusion, and confidence annotation
- generate cold-start codebase summary at scan time

### Performance

- optimize dead-code ground-truth generation ~30x
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

- add structural orientation to sense.status response
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
- add dead_code parameter to sense.graph
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
- add sense.conventions tool to MCP server
## [0.9.0] - 2026-04-20

### Features

- add FTS5 virtual table and keyword search queries
- add HNSW vector index, query embedding, and RRF fusion engine
- add SearchResponse types and MarshalSearch
- add `sense search` command with hybrid search
- add sense.search tool to MCP server
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
