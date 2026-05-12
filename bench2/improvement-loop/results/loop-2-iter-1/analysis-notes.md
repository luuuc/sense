# Loop 2 Iter 1: Deep Transcript Analysis Notes

## Current Scores

| Repo | Sense | Baseline | Gap |
|------|-------|----------|-----|
| axum | 0.9443 | 0.7422 | 0.2021 |
| discourse | 0.7677 | 0.7089 | 0.0588 |
| flask | 0.6480 | 0.5970 | 0.0510 |
| gin | 0.8838 | 0.5932 | 0.2906 |
| javalin | 0.9418 | 0.8174 | 0.1244 |
| nextjs | 0.8447 | 0.7274 | 0.1173 |

Average gap: 0.1407

## Cross-cutting finding: response_richness regex bug (Flask)

The `_SOURCE_FILE_RE` regex in the scorer requires `filename.py:lineref` as a contiguous inline string. Sense outputs structured JSON with `"file"` and `"line"` on separate keys, causing the richness check to count only 1 unique file for sense despite citing 9+ files. This is why flask sense scores LOWER on completeness (0.71 vs 0.89) and discoverability (0.10 vs 0.20). This is a scorer bug, not addressable via scenario improvements — noted for separate fix.

---

## Axum

**Scores:** sense=0.9443, baseline=0.7422, gap=0.2021. Entire gap is tool_fluency (sense=1.0, baseline=0.0). Completeness identical (0.96).

### Step 0: Find Handler trait implementations
- Sense used `sense_graph(Handler, direction=both, depth=2)` → surfaced `HandlerCallWithExtractors` in `axum-extra/src/handler/mod.rs:25` (score=0.92) and `Endpoint::layer` at `routing/mod.rs:796`. Baseline never read `axum-extra`.
- Both hit all 5 checks. No differentiation on content — both 1.0.
- **Proposed:** Add bonus `word: HandlerCallWithExtractors` — rewards exploring the axum-extra extension surface.

### Step 2: Trace serve-to-response lifecycle
- Sense used `sense_search("serve TcpListener accept")` → got `TcpListener::accept` at `listener.rs:39` without reading serve/mod.rs. Baseline read serve/mod.rs 4 times (paginated).
- Baseline found `TowerToHyperService` (hyper bridge type) from `serve/mod.rs:20` import — sense missed this.
- **Proposed:** Add bonus `word: TowerToHyperService` — rewards tracing the hyper integration layer.

### Step 3: Assess request context layer
- Sense cited `MatchedPath`, `OriginalUri`, `ConnectInfo` as architectural precedents for a custom extractor pattern. Baseline did not cite these precedents.
- Both mentioned `route_layer` vs `Router::layer` distinction but it's not a scored check.
- **Proposed:** Add bonus `word: MatchedPath` — rewards citing architectural precedent. Add bonus `word: route_layer` — rewards the practical distinction.

---

## Discourse

**Scores:** sense=0.7677, baseline=0.7089, gap=0.0588. Gap is mostly tool_fluency (0.39 vs 0.0).

### Step 1: Orient
- Sense's `sense_conventions` immediately identified `TopicGuardian` as a distinct module and the serializer→Guardian connection. Baseline didn't name `TopicGuardian` in orientation.
- **Proposed:** Add bonus `word: TopicGuardian`.

### Step 2: Trace topic creation
- CRITICAL: The `starts_with: TopicsController` check is broken. `TopicsController#create` doesn't exist — the real entry is `PostsController#create`. Both agents correctly discovered this. Check guarantees failure for any correct agent.
- Both found `NewPostManager` as the crucial intermediary. Not currently scored.
- **Proposed:** Fix `starts_with` to `PostsController`. Add required `word: NewPostManager`.

### Step 3: Guardian authorization
- Sense's `sense_search("can_create_topic Guardian permission")` surfaced `CurrentUserSerializer#can_create_topic` (score=0.96) — the frontend serializer. Baseline didn't mention serializers in step 3.
- Both found `can_create_topic_on_category` as the intermediate choke point. Not currently scored.
- **Proposed:** Add bonus `word: CurrentUserSerializer`. Add required `word: can_create_topic_on_category`.

### Step 4: Permission check impact
- Sense's `sense_blast(TopicGuardian#can_create_topic?)` returned `GroupedSearchResultSerializer` as an affected symbol — baseline missed it entirely.
- **Proposed:** Add bonus `word: GroupedSearchResultSerializer`. Add `mcp_tool_used: sense_blast`.

---

## Flask

**Scores:** sense=0.6480, baseline=0.5970, gap=0.0510. Sense loses on completeness (0.71 vs 0.89) due to richness regex bug (see cross-cutting finding above).

### Step 1: Trace dispatch pipeline
- Sense produced richer chain detail: `process_response` → `after_this_request` callbacks distinct from `after_request_funcs`, `AppContext.push → _cv_app.set, appcontext_pushed.send`. Baseline missed signal-firing details.
- Sense failed `response_richness` due to JSON formatting (scorer bug).
- **Proposed:** Add bonus `contains: wsgi_app calls full_dispatch_request` — semantic chain check.

### Step 2: Find callers of wsgi_app
- Sense used `sense_graph(Flask.wsgi_app, direction=callers)` → got `Flask.__call__` at `app.py:1618` with `confidence: 1.0`. Also `sense_blast` confirmed `risk=low, direct_callers=1`.
- **Proposed:** Add `mcp_tool_used: sense_graph` bonus. Add `mcp_tool_used: sense_blast` bonus for step 3 (impact).

### Step 4: Debug parameter impact
- Sense identified `FlaskClient` in `testing.py` as affected (baseline didn't). Sense also cited `__init_subclass__` compat shim and noted `wsgi_app` is NOT in the monitored method list.
- **Proposed:** Add bonus `word: FlaskClient`. Add bonus `word: __init_subclass__`.

---

## Gin

**Scores:** sense=0.8838, baseline=0.5932, gap=0.2906. Largest gap. Driven by tool_fluency (1.0 vs 0.0) and completeness (0.92 vs 0.74).

### Step 0: HTTP dispatch
- Sense used `sense_graph(Engine.ServeHTTP, callees)` → `sense_graph(Engine.handleHTTPRequest, callees)` → got full fan-out with line numbers in one round-trip. Baseline read 4 files.
- Already has `mcp_tool_used: sense_graph` check. Both hit all content checks.

### Step 1: Middleware flow
- Sense used `sense_graph(Context.Next, callers)` and `sense_graph(Context.Abort, callers)` for pre-filtered caller lists.
- Both mentioned `combineHandlers` (chain builder) and `abortIndex` (Abort mechanism). Neither is scored.
- **Proposed:** Add bonus `word: abortIndex`. Add bonus `word: combineHandlers`.

### Step 2: Dead code
- Sense used `sense_graph(dead_code=True)` — one call found 6 dead symbols across sub-packages (`binding.validate`, `json.Encoder`). Baseline's grep was limited to root `*.go`.
- `contains: unused` check fails for both (neither used the word "unused"). Both said "dead" or "zero callers".
- **Proposed:** Fix `contains: unused` → `contains: dead` or `contains: zero callers`. Add bonus `contains: binding.validate` — cross-package dead code only sense found.

### Step 3: Recovery middleware
- Sense used `sense_blast(Recovery)` and `sense_blast(CustomRecoveryWithWriter)` → confirmed `gin.Default` as only production caller.
- Both named `Default()` as the registration site — not scored.
- **Proposed:** Add bonus `word: Default` (the registration site).

---

## Javalin

**Scores:** sense=0.9418, baseline=0.8174, gap=0.1244. Gap in tool_fluency (1.0 vs 0.5) and discoverability (1.0 vs 0.8).

### Step 0: Orient
- Sense's `sense_graph(JavalinServlet, callees, depth=2)` and `sense_search` surfaced `TaskInitializer`/`requestLifecycle` — the core architectural concept. Baseline didn't name it.
- **Proposed:** Add bonus `contains: TaskInitializer`.

### Step 1: HTTP dispatch
- Sense traced 3 hops deeper into exception sub-chain: `handleExceptionSafely() → Util.findByClass() → uncaughtException()`. Baseline stopped at `ExceptionMapper.handle()`.
- **Proposed:** Add required `contains: handleTask`. Add bonus `contains: handleExceptionSafely`.

### Step 3: Error handler assessment
- Sense cited specific test `TestExceptionMapper.kt:93` confirming catch-all doesn't override `HttpResponseException`. Baseline didn't cite test evidence.
- Current check `contains: exception` is too weak — trivially passed.
- **Proposed:** Add required `contains: ExceptionMapper`. Add bonus `contains: ErrorMapper`. Add bonus `contains: HttpResponseException`.

---

## Nextjs

**Scores:** sense=0.8447, baseline=0.7274, gap=0.1173. Gap in tool_fluency (0.64 vs 0.0).

### Step 0: Orient
- CRITICAL: `word: BaseServer` check penalizes sense — the class is literally named `Server` at `base-server.ts:316`. Sense used the source-accurate name; baseline used the colloquial "BaseServer". Check error, not capability deficit.
- Sense found `NextCustomServer` (4th class) via graph. Baseline only found 3 classes.
- **Proposed:** Fix `word: BaseServer` to also accept `base-server.ts` (file name is unambiguous). Add bonus `word: NextCustomServer`.

### Step 1: SSR lifecycle
- Sense uniquely traced `router-server.ts:handleRequest` (line 371) as the outermost HTTP entry point. Baseline started at `BaseServer.handleRequest` missing this layer.
- Sense named `renderToHTMLOrFlightImpl` (inner function) distinct from the wrapper. Baseline didn't distinguish them.
- **Proposed:** Add bonus `contains: router-server`. Add bonus `word: renderToHTMLOrFlightImpl`.

### Step 3: Request ID threading
- Sense's `sense_search` surfaced `NEXT_REQUEST_ID_HEADER` and `NEXT_HTML_REQUEST_ID_HEADER` — existing constants — and used `sense_graph` to confirm zero callers (dev-server only stubs). Baseline found them via grep but didn't explain the dev-only scope.
- **Proposed:** Add bonus `word: NEXT_REQUEST_ID_HEADER`. Add bonus `contains: pipeImpl` (cache-safe injection point).
