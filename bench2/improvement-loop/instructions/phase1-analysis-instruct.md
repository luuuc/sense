# Phase 1: Analysis Instructions — Verifiability

You are analyzing bench2 transcripts to identify quality markers and non-differentiating checks.

## Input Files
- `results/loop-N/analysis.json` — automated pattern extraction
- `../../results/{sense,baseline}/{repo}/transcript.json` — raw transcripts
- `../../results/{sense,baseline}/{repo}/scored.json` — scored results

## CRITICAL: Deep Transcript Analysis Required

You MUST read full transcripts before generating improvements. Never generate improvements from metadata alone (tool counts, differentiation stats, scored.json summaries). Metadata tells you WHERE to look; transcripts tell you WHAT to change.

**Use parallel sub-agents** (one per repo) to read transcripts. Reading 12 transcripts sequentially is too slow and leads to shortcuts.

**Per-repo checklist** (repeat for ALL 6 repos — never skip any):
1. Read `scenarios/{repo}.yaml` — understand what each step asks and what checks exist
2. Read `results/sense/{repo}/transcript.json` end-to-end — note every answer, tool call sequence, qualified symbols, file references
3. Read `results/baseline/{repo}/transcript.json` end-to-end — same analysis
4. Read `results/sense/{repo}/scored.json` and `results/baseline/{repo}/scored.json` — verify scores match transcripts
5. Compare side-by-side: Where did sense produce better answers? Where did baseline do equally well? What specific words/phrases did each use?

**Gate: You MUST write `analysis-notes.md` with per-repo findings for ALL repos BEFORE writing improvements.json.** Do not proceed to Phase 2 without this file.

Every improvement must cite specific transcript evidence.

Failure mode to avoid: In loop-1-iter-2, metadata-based improvements caused regressions (axum gap -0.123, flask sense -0.093) because assumed qualified forms weren't reliably produced and thresholds were raised beyond what sense consistently achieves.

## Your Goal

For each scenario, analyze transcripts to find:

1. **Non-differentiating checks**: Checks where both sense and baseline pass (or fail) equally. These are candidates for tightening or replacement.

2. **False positives**: Checks that pass but shouldn't — the answer mentioned a keyword but didn't actually demonstrate understanding. Look for:
   - Words mentioned in passing vs explained in context
   - Symbols listed without file/line references
   - Copy-pasted prompt words that appear in the answer trivially

3. **False negatives**: Checks that fail but shouldn't — the answer demonstrated understanding but used different words. Look for:
   - Paraphrased concepts (e.g., "the entry handler" instead of "ServeHTTP")
   - Correct analysis with slightly different terminology

4. **Verifiable markers**: Patterns in good transcripts that could become checks:
   - Qualified symbol references: "Engine.ServeHTTP in gin.go:123"
   - Cross-file connections: "wsgi_app calls full_dispatch_request"
   - Verification behavior: reading source after MCP results

## Decision Criteria

**Approve tightening** a check if:
- The check is non-differentiating (differentiation ≈ 0.0)
- A more specific form exists in the sense transcript
- The tighter form is likely to appear in 2/3+ of sense runs

**Reject tightening** if:
- The tighter form is fragile (could break on rephrasing)
- The check is already differentiating
- No clear verifiable alternative exists

## Output

Write `improvements.json` to `results/loop-N-iter-M/improvements.json` using the format in SKILL.md.

Focus on:
- Tightening 3-5 checks per scenario that are clearly non-differentiating
- Adding 1-2 `response_richness` threshold increases where both tools easily pass
- Not changing more than 30% of checks in one iteration (incremental improvement)
