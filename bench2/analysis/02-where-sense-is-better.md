# Report 2: Where Sense Is Better

## Summary

Sense's value to the developer is not that the LLM finds more keywords — baseline matches it there. Sense's value is that the LLM produces **more complete impact analysis** (especially multi-hop callers), **burns fewer tokens** getting there, and **follows more directed exploration paths**. The value scales with codebase size and with how many hops deep the question requires.

## 1. Multi-hop caller discovery (the clearest win)

This is the most defensible, factually verifiable advantage. `sense_graph` with depth-2 traversal surfaces transitive callers that grep cannot reach within a reasonable token budget.

### Gin: 3 additional Abort callers

Baseline grepped for `.Abort()` and found 5 direct call sites. Sense's `sense_graph symbol="Context.Abort" direction="callers" depth=2` found those 5 PLUS:
- `defaultHandleRecovery` (recovery.go:110) — calls `AbortWithStatus`, which calls `Abort`
- `BasicAuthForRealm` (auth.go:~64) — calls `AbortWithStatus`
- `BasicAuthForProxy` (auth.go:~112) — calls `AbortWithStatus`

Baseline never mentioned `auth.go` at all. A developer modifying `Abort`'s behavior using baseline's output would have an incomplete picture. The baseline LLM would have needed to grep for `AbortWithStatus` callers after finding that `AbortWithStatus` calls `Abort` — a manual multi-hop traversal it simply didn't do.

### Discourse: CurrentUserSerializer and 3-hop callers

Sense's `sense_blast symbol="TopicGuardian#can_create_topic?"` returned the full transitive chain including:
- `CurrentUserSerializer#can_create_topic` (direct) — **UI-facing impact**: the "New Topic" button disappears for new users. Baseline missed this entirely despite 26 grep calls.
- `can_edit_tags?` (3 hops via `can_edit_topic?` → `can_create_topic_on_category?`) — baseline missed
- `DiscourseAi::Agents::Tools::EditCategory#invoke` (3 hops) — baseline missed
- `TopicsController#timer` (2 hops) — baseline missed

These are callers a developer needs to know about before shipping a permission change. The `CurrentUserSerializer` miss is particularly consequential — it's a frontend regression that would surface in production, not in unit tests.

### Flask: FlaskClient

Sense's blast surfaced `FlaskClient` in `testing.py` as affected by a `wsgi_app` signature change. Baseline missed this. The Sense LLM also correctly interpreted the blast result: when `affected_tests: []` came back (technically true — no test directly calls `wsgi_app`), the LLM explained the indirect path through `FlaskClient → Werkzeug Client → __call__ → wsgi_app` rather than blindly reporting "no tests affected."

## 2. Token and cost efficiency

Sense uses 1.6x fewer total tokens across all repos:

| Repo | Baseline tokens | Sense tokens | Reduction | Cost saving |
|------|---------------:|-------------:|---------:|------------:|
| axum | 677,355 | 242,844 | 2.8x | 17% |
| nextjs | 1,916,470 | 995,844 | 1.9x | 32% |
| gin | 297,032 | 175,092 | 1.7x | 21% |
| javalin | 417,012 | 322,211 | 1.3x | 5% |
| discourse | 1,164,639 | 998,150 | 1.2x | -6% |
| flask | 253,258 | 228,918 | 1.1x | 20% |

**Why this matters for the developer (not just cost):** Fewer tokens = more context window remaining for follow-up questions in the same session. In a real conversation, the developer asks multiple rounds of questions. The LLM with Sense arrives at the same answer with less context consumed, leaving more room for "ok now implement it" or "what about edge case X?"

**Where it doesn't help:** Discourse (Sense was actually more expensive) and small codebases where grep is already fast.

## 3. More directed exploration — less wasted LLM effort

Baseline transcripts show repeated, redundant file reads and dead-end greps:

**Axum baseline:** Read `method_routing.rs` 5 times at different offsets. Read `serve/mod.rs` 5 times. Ran `Glob: **/*.rs` returning 296+ files — a massive token dump. 25 tool calls, ~10 redundant.

**Discourse baseline:** 8-10 dead-end tool calls before finding that topic creation goes through `PostsController`, not `TopicsController`. Started with `grep "def create"` in controllers, then `ls` calls, then more greps in the wrong controller.

**Next.js baseline:** 53 tool calls, 15+ greps on `base-server.ts` with overlapping patterns. Read `base-server.ts` at 12+ different offsets — essentially reading a 3000-line file in 100-line chunks.

Sense sessions are leaner:

**Axum sense:** 16 calls, each file read once or twice. Graph gave the skeleton, then targeted reads.

**Gin sense:** 13 calls, zero grep. Five graph queries mapped directly to the four steps. 48% fewer tokens than baseline.

**Next.js sense:** 41 calls. Graph + search gave structural overview, then targeted reads. 1.9x token reduction.

The Sense overhead is consistent: ~3 calls of initialization ceremony (ToolSearch, sense_status, summary read). After that, Sense calls map 1:1 to questions.

## 4. Better Step 4 (impact/planning) answers

Sense consistently outperforms on the "assess what breaks if you change X" step:

| Repo | Step 4 Baseline | Step 4 Sense | Content gap (after removing mcp_tool_used bonus) |
|------|----------------:|-------------:|--------------------------------------------------|
| flask | 0.778 | 1.000 | Sense found FlaskClient, baseline didn't |
| gin | 0.889 | 1.000 | Sense quantified blast (11 prod + 2 test), baseline qualitative only |
| discourse | 0.714 | 0.857 | Sense found CurrentUserSerializer + 3-hop callers |
| javalin | 0.800 | 1.000 | Near-identical content, gap is mostly mcp_tool_used checks |
| nextjs | 0.750 | 1.000 | Sense found NEXT_REQUEST_ID_HEADER constants |
| axum | 0.778 | 0.778 | Tie |

Step 4 is where the developer gets the most additional value from the LLM having Sense. Impact analysis requires knowing who calls what, transitively — exactly what a pre-built graph provides.

## 5. Quantified blast radius

The Sense LLM gives the developer numbers, not just lists:

- **Gin:** "11 production symbols and 2 test files in the blast radius of CustomRecoveryWithWriter"
- **Discourse:** Structured table of 7+ affected callers with file:line references, labeled "desired" vs "unintended"
- **Next.js:** Reference counts from the index (e.g., "Context has 171 refs")

Baseline's answers are qualitative: "non-breaking", "likely desired but must be confirmed." The developer gets a scope estimate from Sense that they don't get from baseline.

**Caveat (see Report 3):** Some Sense counts are inflated. Axum's "91 BoxedIntoRoute references" is actually 15 — Sense counted graph edges, not source references. Quantification is only valuable if accurate.

## 6. Specific discoveries per repo

Beyond multi-hop callers, Sense found things baseline didn't:

| Repo | Finding | How | Developer impact |
|------|---------|-----|-----------------|
| Discourse | `spam_rules_spec.rb` test breakage at lines 45, 91, 109 | blast caller chain | Prevents CI failures after merge |
| Discourse | `EnsureMagic` method_missing dispatch mechanism | sense_search | Explains how `ensure_can_create!` resolves to `can_create?` |
| Gin | `BasicAuthForRealm`/`BasicAuthForProxy` as Abort callers | sense_graph depth-2 | Complete caller list for behavior change |
| Next.js | `NEXT_REQUEST_ID_HEADER` constants exist in `app-router-headers.ts` | sense_search + sense_graph | Changes plan from "design new" to "wire up existing" |
| Next.js | `NextServer` as third class in server hierarchy | sense_graph | More complete architecture understanding |
| Javalin | `ApiBuilderScope.kt` route-group DSL | sense_graph | Additional route registration surface |

## 7. Value scales with codebase size

| Codebase size | Example | Sense advantage |
|---------------|---------|-----------------|
| Small (<1k symbols) | Flask | None — LLM reads the file and has ground truth |
| Small-medium (~3k) | Javalin, Gin | Moderate — multi-hop callers, efficiency gains |
| Medium (~3k, complex types) | Axum | Mixed — graph helps but can inflate/mislocate |
| Large (58k+) | Discourse | Clear — blast finds 3-hop callers grep can't reach |
| Very large (75k+) | Next.js | Strongest — 1.9x token reduction + unique discoveries |

The pattern is consistent: Sense's value to the developer increases with the distance between "what you can grep" and "what you need to know."
