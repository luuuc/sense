# Phase 2: Generate Improvements

Read LOOP-CONTEXT.md first — it defines the scoring model, improvements.json format, and rules.

## Goal

Using the analysis from Phase 1 (`analysis-notes.md`), generate `improvements.json` that brings each repo's fairness_score gap closer to its qualitative gap.

## Input

- `analysis-notes.md` from Phase 1 — per-repo findings with transcript evidence
- `../../scenarios/{repo}.yaml` — current checks
- `../../results/{sense,baseline}/{repo}/transcript.json` — for verifying evidence
- *(optional)* Scenario-auditor hints from the prior iter — treat as
  candidates to adopt, refine, or reject with rationale; always cite
  transcript evidence in `evidence`, even when the auditor proposed it.

## Process

For each repo where the gap is off by more than 0.03:
1. Review the specific checks identified in analysis-notes.md
2. Write modifications with evidence citations
3. Verify each modification against both transcripts — will it pass/fail as expected?

## Modification Types

- **add_check** — new check that captures a real quality difference
- **remove_check** — check that doesn't correspond to answer quality
- **tighten_check** — make a check more specific to reduce false positives
- **lower_threshold** — reduce response_richness when the threshold is unrealistic
- **raise_threshold** — increase when both tools easily pass

## Constraints

- Every modification must include `evidence` citing specific transcript content
- Never add `mcp_tool_used` or `no_grep` to the fairness layer
- Change no more than 30% of checks per repo per iteration
- Add baseline-favoring checks too — the benchmark must be fair in both directions
- Do not pursue a target gap — pursue accuracy. If the transcripts show equal quality, the gap should be ~0.

## Output

Write `improvements.json` to `results/iter-N/improvements.json`.

Then apply: back up current scenarios, apply modifications to YAML files, re-score.
