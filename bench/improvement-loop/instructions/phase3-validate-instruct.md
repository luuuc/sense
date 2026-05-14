# Phase 3: Validate Improvements

Read LOOP-CONTEXT.md first — it defines the scoring model and convergence criteria.

## Goal

After applying improvements from Phase 2, re-score and verify the fairness_score gaps moved toward transcript quality — not away from it.

## Input

- `results/iter-N/improvements.json` — what was changed
- `../../results/{sense,baseline}/{repo}/scored.json` — scores after re-scoring
- Previous iteration scores for comparison

## Validation Criteria

**Accept improvements** if:
- Per-repo fairness_score gap moved closer to qualitative gap (within 0.03)
- No repo regressed by more than 0.05 on fairness_score
- Correctness dimension didn't drop for either tool (checks are additive, not destructive)

**Reject and revert** if:
- Overall fairness gap moved further from transcript quality
- Any repo regressed more than 0.05
- New checks cause both tools to fail equally (adds noise, not signal)

## Convergence Check

The loop is converged when:
- Fairness score gap stable within 0.02 between iterations
- Per-repo gaps all within 0.03 of qualitative assessment
- No new check gaps identified in transcript review

If converged, stop the loop and report final scores.

## Output

Write validation results:
- Per-repo: old gap → new gap → qualitative target → verdict (closer/further/stable)
- Overall: converged / needs another iteration / revert needed
- If reverting: restore scenario YAMLs from backups
