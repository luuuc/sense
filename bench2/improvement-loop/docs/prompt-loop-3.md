# Loop 3: Weight Optimization — Tune Scoring to Maximize Signal

Read this file, then rigorously follow the instructions below. You are continuing the bench2 improvement loop.

## Context

The bench2 benchmark measures code intelligence tools (Sense MCP) vs baseline (grep/Read). Scoring has 4 dimensions: completeness (0.40), efficiency (0.25), tool_fluency (0.20), discoverability (0.15). Loops 1-2 tightened checks and added semantic depth. Loop 3 tunes the scoring weights so the dimensions that actually differentiate tools carry more weight, and dimensions where both tools score equally carry less.

## Before you start

1. Read the instruction file: `bench2/improvement-loop/instructions/phase3-validate-instruct.md`
2. Read the skill overview: `bench2/improvement-loop/instructions/SKILL.md`
3. Check what iteration you're on by looking at `bench2/improvement-loop/results/` — if `loop-3-iter-1/` exists, you're on iter 2, etc.
4. You need scored results from Loops 1-2 to optimize against. If they don't exist, run Loop 1 first.

## Per-iteration cycle (repeat 3 times)

### Phase 1: Run & Analyze

```bash
cd bench2
bash improvement-loop/scripts/phases/phase1-run-analysis.sh \
  --loop 3 --runs 1 --model sonnet --tool sense,baseline
```

**IMPORTANT: Always run ALL repos. Never start with just one.**

This runs scenarios → scores → writes `improvement-loop/results/loop-3/analysis.json`.

### Deep Analysis (MANDATORY — do not skip or rush)

This is the most important step. You MUST complete the full checklist below for EVERY repo before proceeding to Phase 2. Use parallel sub-agents (one per repo) to read transcripts — reading 12 transcripts sequentially is too slow and leads to shortcuts.

**Per-repo analysis checklist** (repeat for EACH of the 6 repos):

1. Read `scenarios/{repo}.yaml` — understand what each step asks and what checks exist
2. Read `results/sense/{repo}/scored.json` and `results/baseline/{repo}/scored.json` — note per-dimension scores
3. Read `results/sense/{repo}/transcript.json` end-to-end — does the score reflect actual answer quality?
4. Read `results/baseline/{repo}/transcript.json` end-to-end — same check
5. Flag any scores that don't match transcript quality (score inflation or deflation)
6. For each dimension (completeness, efficiency, tool_fluency, discoverability), compute:
   - `sense_score - baseline_score` per dimension
   - Which dimensions contribute most to the overall gap?
   - Which dimensions are wasted (both tools score equally)?
7. Check cross-scenario consistency: does the same dimension differentiate across all repos?

### Gate: Write analysis-notes.md BEFORE Phase 2

Write `improvement-loop/results/loop-3-iter-{N}/analysis-notes.md` with your findings. This file MUST contain:
- Per-repo sections with per-dimension gap calculations
- Transcript evidence for any score inflation/deflation flagged
- Cross-scenario consistency analysis
- Proposed weight changes with rationale tied to transcript evidence

**Do NOT proceed to Phase 2 until analysis-notes.md exists with notes for ALL repos.**

**WARNING**: Never generate improvements from metadata alone. Skipping deep analysis caused regressions in loop 1 iter 2.

### Phase 2: Generate & Apply Improvements

Based on your analysis, write `improvement-loop/results/loop-3-iter-{N}/improvements.json`:

```json
{
  "scenarios": [
    {
      "repo": "gin",
      "modifications": [
        {
          "action": "update_weights",
          "weights": {
            "completeness": 0.35,
            "efficiency": 0.20,
            "tool_fluency": 0.25,
            "discoverability": 0.20
          },
          "rationale": "tool_fluency has the largest per-dimension gap (+0.45). completeness gap is small (+0.05). Shift weight toward the differentiating dimensions."
        },
        {
          "action": "raise_threshold",
          "step_idx": 2,
          "check_type": "response_richness",
          "old_value": "3",
          "new_value": "5",
          "rationale": "Low threshold lets both tools pass easily, reducing discoverability differentiation."
        }
      ]
    }
    ...
  ]
}
```

Then apply:
```bash
bash improvement-loop/scripts/phases/phase2-run-improve.sh --loop 3 --iter {N}
```

### Phase 3: Re-run & Validate

```bash
bash improvement-loop/scripts/phases/phase3-run-validate.sh \
  --loop 3 --iter {N} --runs 1 --model sonnet --tool sense,baseline
```

If regressions are detected, scenarios roll back automatically. Review `improvement-loop/results/loop-3-iter-{N}/regression.json`.

## What to focus on per iteration

- **Iter 1**: Identify the per-dimension gaps. Shift weight from low-gap dimensions to high-gap ones. Keep weights summing to 1.0.
- **Iter 2**: Fine-tune — check if weight changes improved the overall gap. Adjust thresholds on checks that are still non-differentiating. Look for cross-scenario inconsistencies (dimension that differentiates on gin but not flask, etc).
- **Iter 3**: Convergence check. If weight changes between iter 2 and iter 3 are <5%, the loop has converged. Run final validation across all repos.

## Weight optimization guide

| Dimension | Raise weight if... | Lower weight if... |
|-----------|-------------------|-------------------|
| completeness | Tightened checks now differentiate | Both tools still pass most checks equally |
| efficiency | Sense uses significantly fewer tokens | Token counts are similar |
| tool_fluency | Sense uses MCP, baseline uses grep | Both bypass MCP equally |
| discoverability | Sense references more files/connections | Both reference similar depth |

Constraints:
- Weights must sum to 1.0
- No weight below 0.10 (every dimension carries some signal)
- No weight above 0.45 (no single dimension dominates)
- Changes per iteration should be ≤0.05 per weight (gradual tuning)

## Convergence criteria

The loop is converging when:
- Weight changes between iterations < 5% (≤0.05 total absolute change)
- Score gap stable within 0.02 points across runs
- No new anomalies found in transcripts
- Cross-scenario variance < 10%

If converged, the loop can stop early. Note "converged" in improvements.json.

## Success criteria for Loop 3

- Weights converge (< 5% change between final iterations)
- Sense scores in 0.80-0.90 range consistently
- Baseline scores in 0.60-0.70 range consistently
- Score gap ≥ 0.20 points (clear separation)
- Cross-scenario consistency: variance < 10%
- No major regressions (automated check)

## Progress so far

Check `improvement-loop/results/` for completed iterations. Compare before/after gaps in `regression.json` files.
