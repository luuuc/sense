# Report 1: Where Baseline Is Better

## Summary

When the LLM doesn't have Sense, the developer gets a better answer in specific, repeatable situations: small codebases, deep-in-file enumeration, and tasks where reading surrounding code reveals product-level context. These are real advantages the current scoring hides.

## 1. Small codebases: Sense adds nothing

**Flask** is the clearest case. Both LLMs produced factually identical answers — same line numbers, same dispatch chain, same callers. Zero hallucinations on both sides. The codebase is 912 symbols in one main file (`app.py`). When the LLM reads the file directly, it has ground truth. Sense's index confirms what the LLM already knows from reading the source.

| Metric | Baseline | Sense |
|--------|----------|-------|
| Factual errors | 0 | 0 |
| Dispatch chain | Correct | Correct |
| Callers of wsgi_app | Correct (1: `__call__`) | Correct (1: `__call__`) |
| Line number accuracy | All verified | All verified |

**Javalin** is similar. 372 files, 3163 symbols. Both LLMs read the same 8 core files, produced essentially identical dispatch chains, identical error-handling analysis, identical code examples. The answers are interchangeable.

**Takeaway for scoring:** On small codebases, the benchmark should expect near-parity. Any score gap on Flask or Javalin is likely scoring artifact, not real value.

## 2. Deeper enumeration within files

The LLM without Sense reads more of each file and naturally produces more exhaustive listings:

**Flask tests:** Baseline listed 13 specific test functions in `test_basic.py` with line numbers, plus 4 in `test_reqctx.py`, plus 1 in `test_subclassing.py`. The Sense LLM found tests across more files (5 vs 3) but listed fewer specific functions per file. A developer asking "what tests cover dispatch?" gets a more complete answer from baseline for the specific file, but broader coverage from Sense across files.

**Javalin tests:** Baseline listed 6 test files with specific test method names (e.g., "catch-all Exception mapper doesn't override HttpResponseExceptions"). Sense cited similar test names but embedded them as evidence for behavioral claims rather than as an enumeration — arguably more useful framing, but less complete as a list.

**Gin richness:** Baseline referenced 8 unique source files in its answers vs Sense's 4. The LLM without Sense reads files directly and naturally cites more file:line references because that's its primary information source.

**Not currently scored:** No check measures enumeration depth or completeness of test listings.

## 3. Line number accuracy

Across all repos, the baseline LLM's line numbers are more consistently accurate because it reads the file and extracts the number directly. The Sense LLM sometimes misquotes numbers from index results when paraphrasing into prose:

| Repo | Baseline errors | Sense errors |
|------|----------------|--------------|
| Flask | 0 | 0 |
| Gin | 0 | 0 |
| Discourse | Off by 1 (1 instance) | Off by 10 on `can_move_topic_to_category?` (line 71 stated, actual 61) |
| Javalin | 0 | 0 |
| Axum | Off by 1 (1 instance) | `MethodRouter::call_with_state` at 547 (actual 1167 — that's the struct def, not the method) |
| Next.js | "BaseServer" naming (class is `Server`) | Correct class name, but "zero callers" for actively-used headers |

**Axum is the worst case:** Sense cited line 547 for `call_with_state` — that's the struct definition, 620 lines away from the actual method. A developer following that reference wastes time.

## 4. Product-level insights from reading surrounding code

The LLM without Sense reads more surrounding code during grep-based exploration and stumbles into adjacent context that targeted graph queries skip:

**Discourse:** Baseline flagged two insights Sense missed:
- `can_reply_as_new_topic?` does NOT flow through `can_create_topic?` — the proposed 24h check would have a coverage gap
- Changing the check causes hard rejection instead of queuing in `NewPostManager.default_handler` — a product-level tradeoff ("new member gets 'forbidden' error rather than being queued for review")

These come from the LLM reading `new_post_manager.rb` more thoroughly during grep exploration. The Sense LLM got a structural answer from blast and moved on without reading the surrounding implementation logic.

**Flask:** Baseline traced `AppContext.push` → `_get_session` → `match_request` as part of the dispatch pipeline — intermediate steps between `wsgi_app` and `full_dispatch_request`. The Sense LLM jumped straight from `wsgi_app` to `full_dispatch_request`, skipping the context lifecycle.

**This is the serendipity tradeoff:** Broader reading = more context. Targeted queries = faster but narrower. Neither is strictly better — it depends on what the developer needs.

## 5. Dead code detection for constants (Gin)

Baseline found `localhostIP` and `localhostIPv6` in `utils.go` — production constants used only in test files. It found these by grepping for unexported symbols and checking production references.

Sense's `dead_code=true` query returned dead functions and interfaces but missed dead constants. This is a gap in the index's coverage, not in the LLM's reasoning. The Sense LLM found a different valid dead symbol (`waitForServerReady`) but missed the constants entirely.

**The broader point:** The LLM without Sense applies a brute-force grep methodology that covers all symbol types uniformly. The index has coverage gaps that the LLM can't compensate for.

## 6. Wall-clock time on smaller repos

| Repo | Baseline time | Sense time | Winner |
|------|-------------:|----------:|--------|
| axum | 152.9s | 189.7s | Baseline +24% |
| discourse | 181.3s | 193.7s | Baseline +7% |
| javalin | 136.5s | 154.4s | Baseline +13% |
| flask | 105.1s | 74.9s | Sense +29% |
| gin | 129.2s | 116.1s | Sense +10% |
| nextjs | 236.8s | 221.9s | Sense +6% |

Baseline is faster on 3 of 6 repos. Sense's MCP round-trips and initialization overhead (ToolSearch, sense_status, summary read — 3 calls of ceremony) add latency, especially on smaller codebases where grep is already fast.

## 7. Pure knowledge check scores

Stripping all tool-usage checks, looking only at word/contains/exact:

| Repo | Baseline | Sense | Winner |
|------|----------|-------|--------|
| axum | 85% | 85% | TIE |
| discourse | 88% | 94% | Sense |
| flask | 84% | 89% | Sense |
| gin | 95% | 95% | TIE |
| javalin | 100% | 100% | TIE |
| nextjs | 90% | 86% | **Baseline** |

Baseline wins or ties on 4 of 6 repos on pure knowledge. The composite score hides this because tool_fluency (20% weight) and mcp_tool_used checks inflate Sense's numbers.
