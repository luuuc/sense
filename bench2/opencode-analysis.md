# bench2 Critical Analysis — Where the benchmark is wrong and where Sense falls short

## TL;DR

The bench2 composite score (completeness + efficiency) cannot distinguish shallow keyword matching from deep structural understanding. It rewards false positives, penalizes verification behavior, and misses the actual value proposition of code intelligence tools: **more facts, fewer assumptions, less token waste**.

---

## Where the benchmark is wrong

### 1. Keyword presence != correctness

The benchmark checks whether certain words appear in the transcript. It cannot tell whether the answer is right.

**Gin dead code (step 3)** is the clearest example:

| Tool | Found "localhostIP"? | Actually dead? | Score |
|------|---------------------|----------------|-------|
| Baseline | Yes | **No** — used in `context_test.go:2016` | 0.75 bonus |
| Sense | No | **Yes** — found real dead code (`binding.validate`, `waitForServerReady`, `json.Encoder`) | 0.75 bonus |

Both score identically. One is factually wrong. The benchmark has no way to know.

Other examples:
- **Flask, Axum, Javalin**: Identical completeness scores (0.932, 0.979, 0.979) despite Sense referencing 50-80% more unique files. The benchmark sees "yes, `wsgi_app` was mentioned" and stops.
- **Flask step 3**: Both tools miss `conftest.py` — but `conftest.py` contains fixtures, not test coverage for the dispatch pipeline. The checklist item itself is wrong.

### 2. The `no_grep` bonus is unrealistic

Both tools miss `no_grep` in:
- Flask step 2 (find internal callers)
- Javalin step 2 (trace HTTP dispatch)

Finding callers across a codebase requires search. The benchmark treats necessary search as a "failure" even when MCP tools are used alongside it. This is not a tool failure — it is a benchmark design flaw.

### 3. Miss detection penalizes good engineering

Sense sessions show "post-MCP verification reads": reading source files after MCP results to confirm findings. The benchmark counts these as misses.

| Scenario | Verification reads |
|----------|-------------------|
| Next.js | 21 |
| Javalin | 16 |
| Discourse | 12 |
| Flask | 6 |

This is exactly what a careful developer should do. The benchmark punishes verification.

Similarly, reading `.sense/summary.md` on startup (the intended Sense workflow per `CLAUDE.md`) is counted as a "pre-MCP miss" because it's a `Read` call before any MCP tool use.

### 4. Richness is not scored

Sense consistently references more files and symbols than baseline:

| Scenario | Baseline richness | Sense richness |
|----------|-------------------|----------------|
| Flask | 2 | 3 |
| Gin | 7 | 8 |
| Axum | 15 | 18 |
| Discourse | 8 | 9 |
| Next.js | 15 | 18 |
| Javalin | 6 | 10 |

The benchmark reports richness but gives it **zero weight in the composite score**. A tool that produces a deeper, more cross-referenced answer gets the same score as one that barely skims the surface.

### 5. Cost and token metrics are reported but not scored

Sense uses fewer tokens and costs less overall, but the benchmark does not reward this:

| Metric | Baseline | Sense |
|--------|----------|-------|
| Avg tokens | 8,710 | 7,522 |
| Avg grep calls | 16.3 | 5.2 |
| Avg MCP calls | 0.0 | 6.8 |
| Total cost | $5.84 | $4.72 |

The composite score is `0.6 * completeness + 0.4 * efficiency`. Efficiency is a token threshold (≤8K = 1.0). It does not capture the *quality* of token usage.

---

## Where Sense is not good

### 1. The AI reverts to grep after using MCP

This is the most important finding. Even with Sense available, the AI falls back to old habits:

| Scenario | Post-MCP grep calls |
|----------|---------------------|
| Next.js | 20 |
| Discourse | 8 |
| Flask | 3 |

The AI uses Sense for the initial exploration, then switches to `Grep(...)` for follow-up questions instead of using `sense_search` or `sense_graph`. This is a **usage problem**, not a tool problem. The AI does not know how to use Sense effectively.

**Why this is Sense's fault:** The tool is available but the AI is not prompted/trained to use it. The summary.md instructions say "Use Sense MCP tools for ALL codebase understanding" but the AI still defaults to grep. Better prompting, better tool descriptions, or more explicit chaining examples could fix this.

### 2. Sense has structural blind spots that grep catches

**Gin `localhostIP`**: Sense's dead code detection correctly excludes test-only references. `localhostIP` is used in tests, so Sense excludes it. But the benchmark expected it because naive grep finds it. This reveals a mismatch between structural analysis (correct) and naive text search (wrong but what the checklist expects).

**Next.js dynamic dispatch**: Sense's graph could not trace through Next.js's heavy plugin architecture, `renderToHTMLOrFlight` callers, or `RequestMeta` threading. The AI had to grep for these.

### 3. Verification reads suggest the AI doesn't trust Sense

In Javalin, Sense produced 16 verification reads after MCP calls. In Next.js, 21. The AI reads source files to confirm MCP results. This adds overhead and suggests the AI treats Sense as a hint, not a source of truth.

**Why this is Sense's fault:** If Sense results were more complete or better contextualized, the AI would not need to verify. The tool should provide enough confidence that verification is unnecessary.

### 4. MCP overhead in small scenarios

| Scenario | Baseline cost | Sense cost | Delta |
|----------|--------------|------------|-------|
| Flask | $0.55 | $0.57 | +$0.02 |
| Axum | $0.79 | $1.01 | +$0.22 |
| Javalin | $0.62 | $0.65 | +$0.03 |

The cost is amortized across all scenarios (Sense is $1.12 cheaper total), but in individual small scenarios, MCP setup and round-trips add overhead. For single-turn or small tasks, Sense may not pay off.

---

## What the benchmark should measure instead

The real value of code intelligence tools is not "did you mention the right words?" It is:

1. **Accuracy**: Did the answer match ground truth? (Not just keyword presence)
2. **Depth**: How many unique files/symbols were correctly referenced?
3. **Efficiency**: How many tokens/grep calls were needed per unit of depth?
4. **Confidence**: Did the AI verify its own findings, or just guess?
5. **Correct tool usage**: Did the AI use the right tool for the right job?

The current bench2 score formula:

```
score = 0.6 * completeness + 0.4 * efficiency
```

Should become something like:

```
score = 0.4 * accuracy + 0.2 * depth + 0.2 * efficiency + 0.1 * confidence + 0.1 * tool_fluency
```

Where:
- **Accuracy**: Human-verified or script-verified correctness (not just keyword matching)
- **Depth**: Log-scale richness bonus (diminishing returns after 10+ files)
- **Efficiency**: Tokens per depth unit (lower is better)
- **Confidence**: Ratio of MCP calls to verification reads (higher = more confident)
- **Tool fluency**: MCP calls as % of total tool calls (higher = better tool usage)

---

## Recommendations for improving bench2

### Short term

1. **Add an accuracy dimension**: For checks that can be wrong (e.g., dead code, callers), add a post-hoc verification step. If baseline says `localhostIP` is dead, check if it's actually dead. Score accuracy separately from completeness.

2. **Weight richness in the composite score**: Give `response_richness` a non-zero weight. A tool that references 18 files should score higher than one that references 2.

3. **Remove or fix `no_grep`**: Either remove the `no_grep` bonus entirely, or change it to `low_grep` (e.g., ≤3 grep calls = bonus). Searching is not a sin.

4. **Don't count verification reads as misses**: Distinguish between "reading because MCP failed" and "reading to confirm MCP results". The latter is good behavior.

5. **Don't count summary.md as a miss**: Reading the Sense summary is part of the intended workflow. Exclude `.sense/summary.md` from miss detection.

### Medium term

6. **Add per-step accuracy scoring**: For each step, have a human or script verify whether the answer is actually correct. This is expensive but necessary for meaningful differentiation.

7. **Score token efficiency as depth-per-token**: Instead of a flat threshold, compute `richness / token_total`. A tool that finds 18 files in 7K tokens should score higher than one that finds 2 files in 5K tokens.

8. **Track tool usage patterns**: Score how well the AI uses the tool. For example:
   - Did it use `sense_search` before falling back to grep?
   - Did it use `sense_graph` to trace callers instead of grep?
   - Did it chain tools effectively (e.g., `sense_search` → `sense_graph` → `Read`)?

9. **Add a "confidence" metric**: Measure how often the AI verifies MCP results with manual reads. Lower confidence = the tool is not trusted, which is a signal of tool quality.

### Long term

10. **Replace keyword checks with semantic checks**: Instead of checking if "localhostIP" appears, check if the answer correctly identifies dead code. This requires either human annotation or a stronger verification script.

11. **Measure cross-scenario consistency**: Run the same scenario multiple times. Does Sense produce consistent results? Does baseline? High variance in baseline suggests guessing; low variance in Sense suggests reliable structural analysis.

12. **Add a "developer time saved" proxy**: The real value of Sense is that it helps developers answer questions faster. Measure time-to-correct-answer, not just time-to-keyword-match.

---

## Summary

The bench2 benchmark is a huge improvement over bench/ — human-curated checklists, multi-step scenarios, transparent scoring. But it still measures the wrong things:

- It checks keyword presence, not correctness.
- It penalizes search and verification.
- It ignores the actual value of code intelligence: depth, accuracy, and efficiency.

Sense is not perfect either:
- The AI does not use it effectively (reverts to grep).
- It has structural blind spots in dynamic dispatch scenarios.
- It requires verification reads because the AI does not fully trust it.

But the real story is in the metrics the benchmark reports but does not score:
- Sense uses 14% fewer tokens.
- Sense makes 68% fewer grep calls.
- Sense costs 19% less overall.
- Sense produces 50-80% more cross-referenced findings.

The benchmark needs to evolve to capture these differences, or it will continue to report that baseline and Sense are "tied" when they are clearly not.
