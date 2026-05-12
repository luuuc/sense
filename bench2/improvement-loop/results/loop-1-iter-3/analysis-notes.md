# Loop 1 Iter 3 — Deep Transcript Analysis Notes

## Cross-Repo Findings

- 110/118 checks are non-differentiating (both tools pass equally)
- Most score gaps come from tool_fluency (structural penalty for not having MCP), not answer quality
- response_richness is computed on full transcript, not per-step (scorer limitation)
- no_grep as required is harmful — penalizes verification reads and can reward worse answers

## Per-Repo Analysis

### Axum (gap 0.185)
- Both pass all checks identically. Checks are keyword-level.
- Sense found HandlerCallWithExtractors in axum-extra (cross-crate discovery) — baseline missed it.
- Baseline actually scores higher on richness (14 vs 9 unique files) by reading more raw files.
- Both reference ViaParts/ViaRequest M-markers and matchit routing — not currently checked.
- Steps 1-3 checks are named in the prompt, so both tools trivially pass.

### Discourse (gap 0.091)
- starts_with TopicsController is wrong — correct entry is PostsController. Both tools correctly say PostsController.
- Sense blast uniquely found can_edit_topic? being affected by the permission change.
- contains 'ensure' is too loose — both say EnsureMagic specifically.
- Both identify NewPostManager as intermediary — not currently checked.
- no_grep fails for both (sense 13 grep calls, baseline more).

### Flask (gap 0.148)
- No genuine sense advantage — Flask is a single-package codebase where Read(app.py) gives everything.
- Gap comes entirely from tool_fluency scoring.
- response_richness=2 thresholds on steps 0,3 are trivially low.
- Both identify __init_subclass__ for step 3 impact — not currently checked.
- Richness scored values don't match actual file references (scorer bug).

### Gin (gap 0.250)
- 80% of gap comes from tool_fluency (1.0 vs 0.0).
- Step 2 (dead code): baseline found BETTER dead code (localhostIP, localhostIPv6) but scores lower because no_grep is required. False positive for sense.
- Step 0: response_richness=7 fails for both (both get 6 unique files). Was raised too aggressively in iter 1.
- Both reference abortIndex mechanism and serveError function — not currently checked.
- Steps 1,3 answers are substantively identical.

### Javalin (gap 0.191)
- 100% of gap comes from tool_fluency (1.0 vs 0.0).
- Completeness identical (0.9643). Baseline slightly more efficient.
- All checks trivially satisfied by both — keywords in prompts.
- Both identify DefaultTasks lifecycle, ExceptionMapper/ErrorMapper two-layer system — not checked.
- Step 2 has zero required checks — any answer gets 1.0 on score_required.

### Nextjs (gap 0.180)
- Completeness tied (0.975). Gap from tool_fluency + efficiency.
- Baseline actually found MORE callers in step 2 (including PPR background revalidation).
- Baseline traced more pipeline layers in step 1 (router-server.ts, pipeImpl).
- Both reference renderPageComponent, lazyRenderAppPage, AsyncLocalStorage — not checked.
- Efficiency gap is real: sense 27 tool calls, baseline 80.

## Changes Applied

20 total changes across 6 repos (within 30% threshold per repo):
- 4 tighten_check (fix incorrect/loose checks)
- 12 add_check (new verifiable content checks)
- 2 raise_threshold (eliminate trivially-low response_richness thresholds)
- 2 demote/fix (gin no_grep to bonus, gin richness threshold down to 5)
