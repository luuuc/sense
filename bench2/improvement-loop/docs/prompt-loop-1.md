# Loop 1: Verifiability — Tighten Checks to Reduce False Positives

Read this file, then rigorously follow the instructions below. You are continuing the bench2 improvement loop.

## Context

The bench2 benchmark measures code intelligence tools (Sense MCP) vs baseline (grep/Read). Scoring has 4 dimensions: completeness (0.40), efficiency (0.25), tool_fluency (0.20), discoverability (0.15). Most scenario checks are keyword-based (`word` type) and non-differentiating — both tools pass them equally. Loop 1 tightens these checks so they reward actual structural understanding.

## Before you start

1. Read the instruction file: `bench2/improvement-loop/instructions/phase1-analysis-instruct.md`
2. Read the skill overview: `bench2/improvement-loop/instructions/SKILL.md`
3. Check what iteration you're on by looking at `bench2/improvement-loop/results/` — if `loop-1-iter-1/` exists, you're on iter 2, etc.

## Per-iteration cycle (repeat 3 times)

### Phase 1: Run & Analyze

```bash
cd bench2
bash improvement-loop/scripts/phases/phase1-run-analysis.sh \
  --loop 1 --runs 1 --model sonnet --tool sense,baseline
```

**IMPORTANT: Always run ALL repos. Never start with just one.**

This runs scenarios → scores → writes `improvement-loop/results/loop-1/analysis.json`.

### Deep Analysis (MANDATORY — do not skip or rush)

This is the most important step. You MUST complete the full checklist below for EVERY repo before proceeding to Phase 2. Use parallel sub-agents (one per repo) to read transcripts — reading 12 transcripts sequentially is too slow and leads to shortcuts.

**Per-repo analysis checklist** (repeat for EACH of the 6 repos):

1. Read `scenarios/{repo}.yaml` — understand what each step asks and what checks exist
2. Read `results/sense/{repo}/transcript.json` end-to-end — note every answer the AI gave, what tools it used, what qualified symbols it produced, what files it referenced
3. Read `results/baseline/{repo}/transcript.json` end-to-end — same analysis
4. Read `results/sense/{repo}/scored.json` and `results/baseline/{repo}/scored.json` — verify hit/miss data matches what you see in transcripts
5. Compare side-by-side: Where did sense produce richer/more accurate answers? Where did baseline do equally well? What specific words/phrases distinguish them?
6. For each non-differentiating check, verify in the transcripts:
   - Does the sense transcript use a more qualified form? (e.g., `Engine.ServeHTTP` vs `ServeHTTP`)
   - Is the qualified form consistent across the full answer, not just mentioned once?
   - Would tightening this check actually differentiate sense from baseline based on what you read?

### Gate: Write analysis-notes.md BEFORE Phase 2

Write `improvement-loop/results/loop-1-iter-{N}/analysis-notes.md` with your findings. This file MUST contain:
- Per-repo sections with specific transcript evidence
- Which checks are too loose and why (with quotes from transcripts)
- Which new checks would help and why (with quotes from transcripts)
- Any false positives or false negatives found

**Do NOT proceed to Phase 2 until analysis-notes.md exists with notes for ALL repos.**

**WARNING**: Never generate improvements from metadata alone (tool counts, stats summaries). Metadata tells you WHERE to look; transcripts tell you WHAT to change. Skipping deep analysis caused regressions in iter 2 (axum gap -0.123, flask sense -0.093).

### Phase 2: Generate & Apply Improvements

Based on your analysis, write `improvement-loop/results/loop-1-iter-{N}/improvements.json`:

```json
{
  "scenarios": [
    {
      "repo": "flask",
      "modifications": [
        {
          "action": "tighten_check",
          "step_idx": 0,
          "old_check": {"type": "word", "value": "ServeHTTP"},
          "new_check": {"type": "contains", "value": "Engine.ServeHTTP"}
        },
        {
          "action": "raise_threshold",
          "step_idx": 0,
          "check_type": "response_richness",
          "old_value": "5",
          "new_value": "7"
        },
        {
          "action": "promote_to_required",
          "step_idx": 2,
          "check_type": "no_grep",
          "check_value": "no_grep"
        },
        {
          "action": "add_check",
          "step_idx": 0,
          "new_check": {"type": "mcp_tool_used", "value": "sense_graph", "required": false}
        }
      ]
    }
    ...
  ]
}
```

Then apply:
```bash
bash improvement-loop/scripts/phases/phase2-run-improve.sh --loop 1 --iter {N}
```

### Phase 3: Re-run & Validate

```bash
bash improvement-loop/scripts/phases/phase3-run-validate.sh \
  --loop 1 --iter {N} --runs 1 --model sonnet --tool sense,baseline
```

If regressions are detected, scenarios roll back automatically. Review `improvement-loop/results/loop-1-iter-{N}/regression.json`.

## What to focus on per iteration

- **Iter 1**: Tighten 3-5 `word` checks per scenario to `contains` with qualified forms. Raise `response_richness` thresholds where both tools pass easily.
- **Iter 2**: Promote `no_grep` bonus checks to required on structural steps. Add `mcp_tool_used` checks where sense used MCP.
- **Iter 3**: Address any remaining non-differentiating checks. Fix anomalies found in iters 1-2.

## Success criteria for Loop 1

- Non-differentiating checks reduced by 50%+
- Completeness scores start to diverge between sense and baseline
- False positive rate reduced by ~15% per iteration
- No major regressions (automated check)

## Progress so far

Check `improvement-loop/results/` for completed iterations. Compare before/after gaps in `regression.json` files.
