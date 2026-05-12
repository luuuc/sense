# Loop 2: Semantic Depth — Add Checks That Reward Understanding

Read this file, then rigorously follow the instructions below. You are continuing the bench2 improvement loop.

## Context

The bench2 benchmark measures code intelligence tools (Sense MCP) vs baseline (grep/Read). Scoring has 4 dimensions: completeness (0.40), efficiency (0.25), tool_fluency (0.20), discoverability (0.15). Loop 1 tightened keyword checks to reduce false positives. Loop 2 adds semantic checks that reward deeper code understanding — tracing connections, verifying relationships, using structural tools.

## Before you start

1. Read the instruction file: `bench2/improvement-loop/instructions/phase2-improve-instruct.md`
2. Read the skill overview: `bench2/improvement-loop/instructions/SKILL.md`
3. Check what iteration you're on by looking at `bench2/improvement-loop/results/` — if `loop-2-iter-1/` exists, you're on iter 2, etc.

## Per-iteration cycle (repeat 3 times)

### Phase 1: Run & Analyze

```bash
cd bench2
bash improvement-loop/scripts/phases/phase1-run-analysis.sh \
  --loop 2 --runs 1 --model sonnet --tool sense,baseline
```

**IMPORTANT: Always run ALL repos. Never start with just one.**

This runs scenarios → scores → writes `improvement-loop/results/loop-2/analysis.json`.

### Deep Analysis (MANDATORY — do not skip or rush)

This is the most important step. You MUST complete the full checklist below for EVERY repo before proceeding to Phase 2. Use parallel sub-agents (one per repo) to read transcripts — reading 12 transcripts sequentially is too slow and leads to shortcuts.

**Per-repo analysis checklist** (repeat for EACH of the 6 repos):

1. Read `scenarios/{repo}.yaml` — understand what each step asks and what checks exist
2. Read `results/sense/{repo}/transcript.json` end-to-end — note every answer, tool sequence, qualified symbols, semantic chains (A calls B calls C)
3. Read `results/baseline/{repo}/transcript.json` end-to-end — same analysis
4. Read `results/sense/{repo}/scored.json` and `results/baseline/{repo}/scored.json` — verify scores match transcript quality
5. Compare: does sense trace semantic chains that baseline misses? Where did baseline get the keyword right but miss the relationship?
6. Look for MCP tool calls (sense_graph, sense_blast) that produced richer answers — verify by reading the actual answer content, not just tool call metadata

### Gate: Write analysis-notes.md BEFORE Phase 2

Write `improvement-loop/results/loop-2-iter-{N}/analysis-notes.md` with your findings. This file MUST contain:
- Per-repo sections with specific transcript evidence
- Semantic chains sense traced that baseline missed (with quotes)
- MCP tool calls that produced genuinely better answers (with quotes)
- Every proposed improvement must cite specific transcript content

**Do NOT proceed to Phase 2 until analysis-notes.md exists with notes for ALL repos.**

**WARNING**: Never generate improvements from metadata alone. Skipping deep analysis caused regressions in loop 1 iter 2.

### Phase 2: Generate & Apply Improvements

Based on your analysis, write `improvement-loop/results/loop-2-iter-{N}/improvements.json`:

```json
{
  "scenarios": [
    {
      "repo": "gin",
      "modifications": [
        {
          "action": "add_check",
          "step_idx": 0,
          "new_check": {
            "type": "contains",
            "value": "ServeHTTP calls handleHTTPRequest",
            "required": false,
            "description": "Demonstrates understanding of the dispatch chain connection"
          },
          "rationale": "Sense traced the call chain via sense_graph; baseline only listed functions without connecting them."
        },
        {
          "action": "add_check",
          "step_idx": 1,
          "new_check": {
            "type": "mcp_tool_used",
            "value": "sense_graph",
            "required": false,
            "description": "Used structural graph query for caller analysis"
          },
          "rationale": "Sense used sense_graph to find all Context.Next callers; baseline used grep."
        },
        {
          "action": "raise_threshold",
          "step_idx": 1,
          "check_type": "response_richness",
          "old_value": "4",
          "new_value": "6",
          "rationale": "Sense referenced 8 files, baseline referenced 4. Raising threshold rewards deeper exploration."
        },
        {
          "action": "promote_to_required",
          "step_idx": 3,
          "check_type": "mcp_tool_used",
          "check_value": "sense_blast",
          "rationale": "Modification impact assessment is exactly what blast radius analysis is for."
        }
      ]
    }
    ...
  ]
}
```

Then apply:
```bash
bash improvement-loop/scripts/phases/phase2-run-improve.sh --loop 2 --iter {N}
```

### Phase 3: Re-run & Validate

```bash
bash improvement-loop/scripts/phases/phase3-run-validate.sh \
  --loop 2 --iter {N} --runs 1 --model sonnet --tool sense,baseline
```

If regressions are detected, scenarios roll back automatically. Review `improvement-loop/results/loop-2-iter-{N}/regression.json`.

## What to focus on per iteration

- **Iter 1**: Add 2-3 semantic chain checks per scenario (e.g., "A calls B" connection checks). Add `mcp_tool_used` checks where sense used MCP tools that baseline didn't.
- **Iter 2**: Raise `response_richness` thresholds on steps where sense consistently references more files. Promote `no_grep` and `mcp_tool_used` bonus checks to required where they reliably differentiate.
- **Iter 3**: Address remaining steps where both tools produce equally shallow answers. Add `diff_contains` checks for modification steps that require showing specific code changes.

## Improvement types for this loop

| Action | When to use |
|--------|------------|
| `add_check` with `contains` | Sense traces a relationship baseline misses (e.g., "X calls Y") |
| `add_check` with `mcp_tool_used` | Sense used an MCP tool that produced better results |
| `raise_threshold` on `response_richness` | Sense consistently references more files than baseline |
| `promote_to_required` | A bonus check reliably passes for sense but not baseline |
| `tighten_check` | A word check passes for both — tighten to qualified form |

## Success criteria for Loop 2

- Semantic chain checks added to 60%+ of steps
- MCP tool usage checks on all steps where sense used MCP
- Discoverability scores diverge (sense references more files)
- Score gap widens by 0.10-0.15 over Loop 1 baseline
- No major regressions (automated check)

## Progress so far

Check `improvement-loop/results/` for completed iterations. Compare before/after gaps in `regression.json` files.
