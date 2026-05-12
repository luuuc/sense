# Bench2 Improvement Loop Context

You are driving an autonomous benchmark improvement process. The benchmark measures whether a developer gets a better answer with Sense (a code-intel tool) vs without it.

## Two-Layer Scoring

The scorer produces two independent scores:

**Fairness score** (primary — used for Sense vs Baseline comparisons):
- **Correctness** (0.70) — checklist hit rate, excluding checks tagged `layer: adoption`
- **Efficiency** (0.30) — token waste calibrated per repo size (Flask 15k, Next.js 40k)

**Adoption score** (secondary — for code-intel-vs-code-intel comparisons only):
- Tool fluency + discoverability. Excluded from fairness comparisons.

## What the Loop Optimizes

The loop improves scenario YAML checks so that the **fairness score gap** between Sense and Baseline accurately reflects the qualitative difference visible in transcripts.

It does NOT optimize for:
- Maximizing Sense's score or the gap
- MCP tool usage or adoption metrics
- Target score ranges ("Sense 0.8-0.9, baseline 0.6-0.7")

## Success Criterion

**Fairness score gap matches qualitative transcript analysis within 0.03.**

If reading the transcripts side-by-side suggests Sense gave a ~5% better answer on a repo, the fairness_score gap for that repo should be 0.04-0.06, not 0.15 or 0.00.

## Loop Structure

Single loop, N iterations to convergence. Each iteration:
1. **Analyze** — read transcripts, compare answers, identify check gaps
2. **Improve** — generate `improvements.json` with evidence-cited changes
3. **Validate** — re-score, verify gap accuracy, check for regressions

Converged when fairness score gap is stable within 0.02 between iterations and matches transcript quality assessment.

## Check Categories (Guide Check Authoring)

These 5 dimensions from the analysis doc guide which checks to write. They are NOT separate scored dimensions — the scorer only has correctness + efficiency.

1. **Hallucinations** — wrong file paths, invented functions, inflated counts
2. **Correctness** — call chains, callers, architectural relationships
3. **Actionability** — file:line refs, test breakage warnings, impact completeness
4. **Serendipity** — unique discoveries from broader/targeted reading
5. **Efficiency** — token waste, redundant exploration

## improvements.json Format

```json
{
  "scenarios": [
    {
      "repo": "gin",
      "modifications": [
        {
          "action": "add_check",
          "step_idx": 1,
          "evidence": "Sense transcript found BasicAuthForRealm as transitive Abort caller; baseline missed it",
          "new_check": {"type": "word", "value": "BasicAuthForRealm", "required": false}
        },
        {
          "action": "tighten_check",
          "step_idx": 0,
          "evidence": "Both transcripts use 'Engine.ServeHTTP' — tighter form still passes both",
          "old_check": {"type": "word", "value": "ServeHTTP"},
          "new_check": {"type": "contains", "value": "Engine.ServeHTTP"}
        },
        {
          "action": "remove_check",
          "step_idx": 2,
          "evidence": "can_edit_tags not found in either transcript — neither tool surfaces this",
          "check_type": "word",
          "check_value": "can_edit_tags"
        }
      ]
    }
  ]
}
```

Every modification MUST include an `evidence` field citing specific transcript content.

## Rules

- Never add `mcp_tool_used` or `no_grep` checks to the fairness layer. These belong in `layer: adoption` only.
- Never reference tool adoption, MCP incentives, or target score ranges in improvements.
- Every improvement must cite specific transcript evidence — not metadata or scored.json summaries.
- Change no more than 30% of checks per iteration.
- Read FULL transcripts side-by-side before generating improvements.
