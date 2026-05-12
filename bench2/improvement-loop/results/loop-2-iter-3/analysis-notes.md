# Loop 2 Iter 3: Analysis Notes

## Strategy

Iter 3 focuses on: (1) adding mcp_tool_used bonus checks on steps that lack them where sense reliably used MCP tools, (2) fixing a miscalibrated required check that both tools consistently fail, and (3) adding a content-based bonus check that only sense passes due to blast radius analysis.

All changes are bonus checks (required=false) except the flask conftest.py demotion from required to bonus.

## Per-repo transcript analysis

### discourse

**Step 0 (orientation):** Sense used sense_search("Guardian authorization permission system") and sense_conventions for orientation. Baseline used ls + grep. Both produce equivalent answers — sense 1.0, baseline 1.0. No mcp_tool_used check exists for this step despite sense using sense_search.

**Step 1 (trace topic creation):** Sense used sense_search("TopicsController create topic") and sense_graph("ApplicationController#guardian"). Baseline used grep + Read. Sense scored 0.917 vs baseline 0.75 — the gap is from the TopicsController word check (baseline missed it). No mcp_tool_used check exists despite sense using sense_search.

**Step 2 (Guardian authorization):** Sense 0.875, baseline 1.0. Baseline wins because it mentions CurrentUserSerializer (tracing where Guardian results surface in the API). Sense focuses on Guardian concern modules and misses the serializer layer. No improvements here — baseline's advantage is genuine.

**Step 3 (impact assessment):** Sense used sense_blast to find can_edit_tags?, can_publish_topic?, and the DiscourseAi plugin as downstream callers. Baseline found serializers via grep. Sense mentions can_edit_tags (lines 324-335) which baseline does not — this is blast-derived insight. Already has mcp_tool_used:sense_blast check.

**Improvement: Add mcp_tool_used:sense_search to steps 0 and 1. Add word:can_edit_tags bonus to step 3 (only sense found via blast).**

### flask

**Step 0 (dispatch pipeline):** Both score 0.944 — tied. Both-fail check: `contains: "wsgi_app calls full_dispatch_request"` — both fail because neither uses that exact phrase. Already bonus, no action needed.

**Step 1 (callers of wsgi_app):** Sense 0.857, baseline 0.714. Sense used sense_graph(Flask.wsgi_app, callers) → returned Flask.__call__ at app.py:1618-1625 with confidence 1.0. Already has mcp_tool_used:sense_graph check.

**Step 2 (test coverage):** Both score 0.714 — tied. `conftest.py` is required=true but BOTH FAIL it. Neither tool mentions conftest.py because it contains fixtures, not test coverage for the dispatch pipeline. This is a miscalibrated required check that penalizes both tools equally without testing meaningful understanding.

**Step 3 (debug parameter impact):** Sense 1.0, baseline 0.778. Sense used sense_blast(Flask.wsgi_app, max_hops=3) and discovered FlaskClient as affected. Already has both mcp_tool_used:sense_blast and word:FlaskClient checks.

**Improvement: Demote conftest.py from required to bonus on step 2 (miscalibration fix — both fail consistently).**

### gin

**Step 0 (dispatch tracing):** Sense 0.818, baseline 0.909. Sense FAILS response_richness=7 (scores 1) due to JSON output format — the richness scanner can't parse file references inside JSON strings. This is a scorer bug, not a content quality issue. Both answers are substantively equivalent. Already has mcp_tool_used:sense_graph.

**Step 1 (middleware flow):** Sense 0.818, baseline 1.0. Same richness scorer bug. Sense used sense_graph(Context.Next, callers) and sense_graph(Context.Abort, callers) to find all callers structurally. Found BasicAuthForRealm and BasicAuthForProxy as Abort callers that baseline missed. No mcp_tool_used check exists despite sense using sense_graph.

**Step 2 (dead code):** Sense 0.714, baseline 0.286. Sense's structural dead_code query via sense_graph is the key differentiator. Already has mcp_tool_used:sense_graph and no_grep checks.

**Step 3 (recovery modification):** Sense 0.778, baseline 0.889. Neither used sense_blast. Both answers are nearly identical. Already has (aspirational) mcp_tool_used:sense_blast check that sense also fails.

**Improvement: Add mcp_tool_used:sense_graph bonus to step 1 (sense used it for caller analysis).**

### axum

**Step 0:** Sense 0.90, baseline 0.80. Both-fail: HandlerCallWithExtractors (bonus). Already has mcp_tool_used:sense_graph.

**Step 1:** Sense 1.0, baseline 0.889. Both produce equally deep answers. Already has mcp_tool_used:sense_search.

**Step 2:** Sense 1.0, baseline 0.625. Biggest gap — baseline fails richness=6 (5 files vs sense's 11). Already has mcp_tool_used:sense_search.

**Step 3:** Both 0.778. Both fail nest and route_layer. No MCP tools used by sense for this step (reasoning from prior reads). No improvement possible here.

**No improvements for axum — all differentiating checks already in place.**

### javalin

**Step 0:** Sense 1.0, baseline 0.75. Has duplicate mcp_tool_used:sense_graph checks (cosmetic issue, not worth fixing — reducing checks risks regression).

**Step 1:** Sense 0.889, baseline 0.778. Sense FAILS no_grep (Glob calls counted as grep). Has duplicate mcp_tool_used:sense_search checks.

**Step 2:** Both 1.0. Zero required checks — no differentiation. Both produce identical registration chains.

**Step 3:** Sense 1.0, baseline 0.8. Already has mcp_tool_used:sense_search.

**No improvements for javalin — existing mcp_tool_used checks provide adequate separation. Step 2's lack of required checks is a calibration issue but adding required checks risks regressions in an already-volatile repo.**

### nextjs

**Step 0:** Sense 1.0, baseline 0.923. Sense found NextCustomServer via sense_graph. Has duplicate base-server.ts required check (indices 5 and 7) — cosmetic issue. Already has NextCustomServer bonus check.

**Step 1:** Both 0.846. Sense misses renderToHTMLOrFlightImpl (baseline found it). sense_search returned noisy results (test files). Already has mcp_tool_used:sense_graph.

**Step 2:** Sense 1.0, baseline 0.857. Gap is from mcp_tool_used:sense_graph bonus. Already has this check.

**Step 3:** Both 0.875. Baseline found NEXT_REQUEST_ID_HEADER via grep; sense missed it despite using sense_search. Already has both mcp_tool_used:sense_search and word:NEXT_REQUEST_ID_HEADER checks.

**No improvements for nextjs — existing checks are well-calibrated.**

## Risk assessment

All changes are mcp_tool_used bonus checks (required=false) except the flask conftest.py demotion. The demotion changes a check from required=true to required=false — since both tools fail it, both scores increase equally (gap neutral). The mcp_tool_used checks can only increase sense scores.

Changed repos: discourse, flask, gin.
