# Phase 3: Validation Instructions — Weight Optimization

You are reviewing delta metrics to validate improvements and optimize scoring weights.

## Input Files
- `results/loop-N-iter-M/post-analysis.json` — analysis after re-run
- `results/loop-N-iter-M/regression.json` — automated regression check
- `results/loop-N/analysis.json` — analysis before changes (baseline)
- `../../results/{sense,baseline}/{repo}/scored.json` — current scores

## Your Goal

1. Review whether improvements actually increased score differentiation
2. Identify which scoring dimensions contribute most to differentiation
3. Propose weight adjustments for the next iteration
4. Decide whether to keep improvements or suggest further changes

## Validation Criteria

**Approve improvements** if:
- Overall quality score gap improved ≥ 5%
- No major regressions (>10% drop on any scenario)
- False positive rate decreased or stable
- Token efficiency maintained

**Reject and recommend revert** if:
- Overall gap decreased
- Major regressions detected
- False positives increased > 5%
- MCP usage decreased

## Weight Optimization

Analyze which dimensions contribute most to accurate differentiation:

| Dimension | What it measures | Adjust if... |
|-----------|-----------------|-------------|
| completeness | Keyword/check hit rate | Both tools score equally → lower weight |
| efficiency | Token usage | Already differentiates well → keep or lower |
| tool_fluency | MCP vs grep ratio | Key differentiator → consider raising |
| discoverability | Files/connections surfaced | Varies by scenario → tune per-scenario |

Propose weight changes in improvements.json:
```json
{
  "action": "update_weights",
  "weights": {"completeness": 0.35, "efficiency": 0.20, "tool_fluency": 0.25, "discoverability": 0.20}
}
```

## Convergence Check

The loop is converging when:
- Weight changes between iterations < 5%
- Score gap stable within 0.02 points
- No new anomalies found in transcripts

If converged, the loop can stop early.

## Output

Write `improvements.json` with weight adjustments for the next iteration,
or note "converged" if no further changes needed.
