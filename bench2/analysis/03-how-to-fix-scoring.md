# Report 3: How to Fix Scoring — The Five Dimensions

## The Core Problem

The current scoring asks "did the LLM use Sense?" (89% of the score gap) instead of "did the developer get a better answer?"

Bench2 was designed to be a human-understandable benchmark — a bench a human could do and judge. But the scoring drifted toward measuring Sense adoption (tool_fluency, mcp_tool_used checks) rather than output quality. This conflates two distinct questions:

1. **"Does the developer get a better answer?"** — the benchmark question (baseline vs Sense vs any tool)
2. **"Does the LLM actually pick up Sense?"** — the adoption question (Sense vs Roam vs Greptile)

Tool fluency is valuable for question 2 — comparing code-intel tools against each other. But it's meaningless against baseline. The scoring needs to separate these.

## The Five Dimensions

The transcript analysis revealed five dimensions that actually capture whether the developer gets a better answer. These should replace the current composite score for fairness comparisons.

### Dimension 1: Hallucinations

**What it measures:** Does the LLM with Sense make fewer factual errors? Wrong file paths, wrong line numbers, invented functions, misattributed behavior.

**What the transcripts show:**

| Repo | Baseline hallucinations | Sense hallucinations |
|------|------------------------|---------------------|
| Flask | 0 | 0 |
| Gin | 0 | 0 |
| Javalin | 0 | 0 |
| Discourse | 1 (off-by-1 line) | 2 (off-by-10 line, misquoted line from index) |
| Axum | 1 (Extension location conflation) | 3 (Extension wrong file, MethodRouter line 547 vs 1167, "91 refs" actually 15) |
| Next.js | 1 (class named "BaseServer" vs actual "Server") | 1 ("zero callers" for actively-used headers) |

**Surprise finding: Sense doesn't reduce hallucinations — it can increase them.** When the index returns incomplete or edge-counted data, the LLM states it as ground truth without verification. Three categories:

1. **Line number misquoting:** Sense blast/graph returns correct `line_start` values, but the LLM rounds or misquotes when paraphrasing into prose (Discourse: line 71 stated, actual 61)
2. **Edge count inflation:** Sense counts graph edges (callers + callees + type-refs), LLM reports as source references (Axum: "91 BoxedIntoRoute references" is actually 15)
3. **False negatives as ground truth:** Index returns empty results, LLM states "zero callers" when grep would find real usage (Next.js: NEXT_REQUEST_ID_HEADER has active callers in app-render.tsx and fetch-server-response.ts)

**The false confidence problem:** The LLM trusts index results without cross-checking with grep. The CLAUDE.md instruction "verify a sample with grep before finalizing" was NOT followed in any Sense transcript. This is the opposite of what should happen — Sense should make the LLM MORE accurate, but incomplete indices make it LESS accurate because the LLM doesn't hedge.

**How to score it:**
```
hallucination_score = 1.0 - (verified_errors / total_factual_claims)
```
Requires manual verification of a sample of file:line claims per transcript. Could be partially automated by checking if cited line numbers contain the claimed symbol.

### Dimension 2: Correctness

**What it measures:** Does the LLM get the actual technical content right? Call chains, callers vs callees, function behavior, architectural relationships.

**What the transcripts show:**

Both LLMs get the core call chains right in every repo. The dispatch pipeline (Step 1), middleware flow (Step 2), and basic understanding (Step 3) are correct on both sides. The differentiation is in **completeness of caller lists** and **depth of impact analysis**:

| Repo | Baseline callers found | Sense callers found | Sense delta |
|------|----------------------:|--------------------:|-------------|
| Flask | 1 (correct, complete) | 1 (correct, complete) | Tie |
| Gin (Abort) | 5 direct | 8 (5 direct + 3 transitive) | +3 callers |
| Discourse (can_create_topic?) | ~5 | ~9 (including 3-hop) | +4 callers |
| Javalin | Same | Same | Tie |
| Axum | Same | Same | Tie |
| Next.js (renderToHTMLOrFlight) | Same direct callers | Same + indirect chain | Slightly richer |

**Sense's correctness advantage is in multi-hop completeness**, not in basic accuracy. The LLM with Sense doesn't understand the code better — it sees further into the dependency graph.

**How to score it:**
- Maintain current `word`/`contains` checks for required symbols in the call chain
- Add **caller completeness checks**: specific non-obvious callers that require 2+ hop traversal
- Promote current bonus checks to required where they represent real impacts (e.g., `CurrentUserSerializer` in Discourse, `FlaskClient` in Flask)

### Dimension 3: Actionability

**What it measures:** Can the developer take the LLM's answer and act on it? Specific file:line references, concrete code suggestions, complete impact lists, test file identification.

**What the transcripts show:**

Both LLMs produce actionable output. The differences are subtle:

**Sense wins on impact completeness:**
- Discourse: Sense warned about `spam_rules_spec.rb` breakage at specific lines (45, 91, 109). Baseline didn't mention it. Developer using baseline's answer ships a change that breaks CI.
- Flask: Sense identified FlaskClient as affected. Developer using baseline's answer misses test infrastructure impact.
- Gin: Sense quantified blast radius ("11 production symbols, 2 test files"). Developer using baseline gets "non-breaking" — less precise.

**Baseline wins on enumeration depth:**
- Flask: 13 specific test functions vs Sense's broader-but-shallower coverage
- Javalin: More specific test method names as a complete list

**Baseline wins on product-level context:**
- Discourse: "new member gets forbidden error rather than being queued for review" — a product decision, not a code question
- Discourse: `can_reply_as_new_topic?` gap identification

**How to score it:**
- **Impact completeness:** Count of real downstream impacts identified (verifiable against the actual codebase graph)
- **Test breakage prediction:** Did the LLM warn about specific tests that would break?
- **Actionable code suggestions:** Did the answer include the actual code change, not just advice?
- **Product-level tradeoffs:** Did the LLM flag behavioral changes the developer should decide on? (harder to automate)

### Dimension 4: Serendipity Tradeoff

**What it measures:** The LLM without Sense reads more surrounding code and stumbles into adjacent context. The LLM with Sense is more targeted. Does the broader reading help? Does the targeting miss anything?

**What the transcripts show:**

| Repo | Baseline serendipity finds | Sense targeting finds | Net |
|------|---------------------------|----------------------|-----|
| Flask | `AppContext.push` lifecycle, `test_reqctx.py` depth | `test_blueprints.py`, `test_async.py`, FlaskClient | Wash |
| Gin | `localhostIP` dead constants | `BasicAuthForRealm`/`Proxy`, `binding` dead code | Sense (higher-value finds) |
| Discourse | queue-vs-reject product decision, `can_reply_as_new_topic?` gap | `CurrentUserSerializer`, `can_edit_tags?`, `spam_rules_spec.rb` | Sense (more impact-relevant) |
| Javalin | None unique | `ApiBuilderScope.kt` | Marginal Sense |
| Axum | `OriginalUri` extractor | `NestedPath` extractor, `ConnectInfo` precedent pattern | Wash |
| Next.js | Image optimization path, Edge runtime path | `NEXT_REQUEST_ID_HEADER`, `NextServer`, `WorkStore` threading | Sense (unique architecture finds) |

**Pattern:** Baseline's serendipity produces product-level and edge-case insights (queue behavior, image paths). Sense's targeting produces structural and impact-relevant insights (callers, blast radius, existing infrastructure). For a developer planning a code change, Sense's finds are more likely to prevent bugs. Baseline's finds are more likely to inform design decisions.

**How to score it:**
This is the hardest dimension to automate. Possible approaches:
- **Unique findings check:** Per-repo checks for insights that only one approach typically surfaces
- **Baseline-favoring checks:** Add checks for product-level insights that require reading surrounding code (e.g., `can_reply_as_new_topic?` gap in Discourse)
- **Sense-favoring checks:** Add checks for multi-hop discoveries (already partially covered by caller completeness)
- Accept that some repos will naturally favor one approach

### Dimension 5: Wasted Context

**What it measures:** How much of the LLM's token budget was spent on exploration overhead vs producing the answer? Redundant file reads, dead-end greps, initialization ceremony.

**What the transcripts show:**

| Repo | Baseline waste | Sense waste | Pattern |
|------|---------------|------------|---------|
| Flask | 1 redundant grep of 13 calls | 3 init calls of 11 | Sense overhead = init ceremony |
| Gin | Moderate (multiple grep rounds for dead code) | 3 init calls of 13 | Baseline wastes on iteration |
| Discourse | 8-10 dead-end calls finding wrong controller | 5-7 low-value calls (noisy search, conventions) | Both waste differently |
| Javalin | 4 calls locating source dirs | 3 init + 2 redundant graph calls | Near-tie |
| Axum | ~10 redundant reads (same files 5-6x) | 3 init calls | Baseline wastes on re-reads |
| Next.js | ~15-20 dead-end/redundant greps on base-server.ts | 5-7 overhead (init + post-MCP verification greps) | Baseline wastes significantly more |

**Token waste ratio (total tokens / billed output tokens):**

| Repo | Baseline ratio | Sense ratio | Interpretation |
|------|---------------:|------------:|----------------|
| Flask | 33x | 51x | Both low-waste (small codebase) |
| Gin | 34x | 21x | Sense much leaner |
| Discourse | 109x | 79x | Both high (large codebase), Sense better |
| Javalin | 46x | 31x | Sense leaner |
| Axum | 72x | 21x | Sense dramatically leaner |
| Next.js | 138x | 75x | Both high, Sense nearly 2x better |

**Sense's consistent overhead:** 3 initialization calls (ToolSearch, sense_status, summary read) appear in every transcript. This is ~fixed cost regardless of codebase size. On large codebases, it's negligible. On Flask, it's a meaningful fraction of total calls.

**How to score it:**
```
efficiency = 1.0 - (total_tokens / ceiling_for_repo_size)
```
Calibrate ceiling per repo:
- Flask/Gin/Javalin: 15,000 billed tokens
- Axum: 20,000
- Discourse: 30,000
- Next.js: 40,000

Current scorer uses fixed 8k-60k range for all repos, which doesn't account for the fact that Next.js legitimately requires more tokens than Flask.

---

## Structural Scoring Fixes

### Fix 1: Two-layer scoring

```
fairness_score = weighted(hallucination, correctness, actionability, serendipity, efficiency)
                 → for baseline vs Sense comparisons

adoption_score = tool_fluency + discoverability
                 → for Sense vs Roam vs Greptile comparisons (only when comparing code-intel tools)
```

Tool fluency answers "does the LLM pick up Sense vs ignoring it?" — useful for comparing Sense against other code-intel tools. Meaningless against baseline because baseline can't use MCP tools.

### Fix 2: Remove tool-usage checks from fairness layer

Currently `mcp_tool_used` and `no_grep` checks appear inside completeness:

| Repo | Tool-usage checks | % of all checks |
|------|------------------:|----------------:|
| javalin | 6 | 30% |
| gin | 5 | 17% |
| discourse | 4 | 16% |
| nextjs | 4 | 14% |
| flask | 3 | 12% |
| axum | 3 | 12% |

Move these to the adoption layer. They inflate Sense's completeness score — Javalin's 0.262 score gap is almost entirely manufactured by its 30% tool-usage checks.

### Fix 3: Add hallucination/correctness checks

**Automated hallucination detection (partial):**
- For each file:line claim in the transcript, check if the cited line contains the claimed symbol
- Count verified vs total claims
- Flag "confident structural claims" from Sense that contradict grep (the "zero callers" problem)

**Caller completeness checks (per repo):**
```yaml
# Gin Step 4: Callers that require 2+ hops
- type: word
  value: BasicAuthForRealm
  required: true
  description: Transitive Abort caller via AbortWithStatus — requires multi-hop traversal

# Discourse Step 4: UI-facing impact
- type: word
  value: CurrentUserSerializer
  required: true   # promote from bonus — this is a production UI regression
  description: Frontend "New Topic" button visibility affected

# Flask Step 4: Test infrastructure impact
- type: word
  value: FlaskClient
  required: true   # promote from bonus — affects all test suites
  description: FlaskClient calls wsgi_app via __call__
```

### Fix 4: Add actionability checks

```yaml
# Discourse: Test breakage prediction
- type: contains
  value: spam_rules_spec
  required: false
  description: Warned about specific test breakage from permission change

# Next.js: Existing infrastructure discovery
- type: word
  value: NEXT_REQUEST_ID_HEADER
  required: true   # promote — changes the implementation plan entirely
  description: Found existing but unused request ID infrastructure

# Discourse: Product-level tradeoff (baseline-favoring)
- type: contains
  value: reply_as_new_topic
  required: false
  description: Identified permission gap in topic reply path

# Discourse: Queue-vs-reject decision (baseline-favoring)
- type: contains
  value: queue
  required: false
  description: Flagged behavioral change from rejection to queueing
```

### Fix 5: Calibrate efficiency per repo size

Current: linear 1.0 at ≤8k, 0.0 at 60k for all repos.

Proposed: scale by codebase size.

| Repo | Symbols | Par tokens | Ceiling |
|------|--------:|-----------:|--------:|
| Flask | 912 | 5,000 | 15,000 |
| Gin | ~2,000 | 6,000 | 15,000 |
| Javalin | ~3,000 | 7,000 | 15,000 |
| Axum | ~3,000 | 8,000 | 20,000 |
| Discourse | ~58,000 | 12,000 | 30,000 |
| Next.js | ~75,000 | 15,000 | 40,000 |

### Fix 6: Remove stale/broken checks

| Check | Repo | Problem | Action |
|-------|------|---------|--------|
| `HandlerCallWithExtractors` | Axum | Both miss — may not exist in tested version | Verify or remove |
| `can_edit_tags` | Discourse | Neither finds — unclear blast relevance | Remove |
| `conftest.py` | Flask | pytest fixture, not dispatch test | Replace with `test_reqctx` |
| `"wsgi_app calls full_dispatch_request"` | Flask | Both describe the relationship differently | Relax to `contains: full_dispatch_request` (already a separate check) or remove |
| `BaseServer` word check | Next.js | Actual class name is `Server` — Sense uses correct name, gets penalized | Change to `contains: base-server` (the file name) |
| `response_richness >= 7` | Gin Step 1 | Threshold too high for index-based approach | Lower to 4 |
| `response_richness >= 6` | Axum Step 3 | Same issue | Lower to 4 |

### Fix 7: Add baseline-favoring checks

The benchmark must be fair in both directions. Add checks that reward things baseline does better:

```yaml
# Flask: Context lifecycle (baseline traces this, Sense skips)
- type: word
  value: AppContext
  required: false
  description: Traced context push lifecycle as part of dispatch

# Discourse: Queue behavior
- type: contains
  value: queue
  required: false
  description: Flagged queue-vs-reject behavioral impact

# Gin: Dead constants
- type: word
  value: localhostIP
  required: false   # keep as bonus — valid but specific
  description: Found dead production constant used only in tests
```

---

## What the Fair Score Would Tell

With these fixes, the benchmark tells the real story:

**Small codebases (Flask, Javalin):** Near-parity. The LLM with Sense and without produce equivalent answers. Sense saves some tokens but doesn't change what the developer gets. Score gap: ~0.

**Medium codebases (Gin, Axum):** Moderate Sense advantage on impact analysis. Sense finds transitive callers that baseline misses. But Sense can also inflate numbers or mislocate symbols. Score gap: ~+0.05-0.10 for Sense.

**Large codebases (Discourse, Next.js):** Clear Sense advantage. Multi-hop blast radius, unique discoveries, significant token savings. But with the false-confidence risk when the index is incomplete. Score gap: ~+0.10-0.15 for Sense.

**Average fair gap: ~+0.05-0.08** (vs current inflated +0.159).

The story becomes: *"On small codebases, Sense is a wash. On large codebases, the LLM with Sense gives the developer more complete impact analysis, burns fewer tokens, and finds structural relationships that grep can't reach — but the LLM must verify index results against the source, because incomplete indices create false confidence."*

That's a true, nuanced, defensible claim. Much stronger than "Sense scores 0.84 vs baseline 0.68" which is mostly measuring tool availability.
