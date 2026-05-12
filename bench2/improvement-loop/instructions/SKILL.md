# Bench2 Improvement Loop Skill

You are driving an autonomous benchmark improvement process. Your goal is to make bench2 scores accurately reflect real tool quality through 9 iterations of analysis, improvement, and validation.

## Workflow

For each loop (1-3):
  1. Run Phase 1: Run scenarios (ALL repos — never just one), score, generate analysis.json
  2. **Deep Analysis (MANDATORY)**: Use parallel sub-agents (one per repo) to read FULL sense and baseline transcripts side-by-side for ALL 6 repos. Compare actual answers, tool sequences, qualified symbols, file references.
  3. **Gate**: Write `analysis-notes.md` with per-repo findings for ALL repos. Do NOT proceed without this file.
  4. Run Phase 2: Generate evidence-based improvements.json (every improvement must cite transcript evidence), apply to scenarios
  5. Run Phase 3: Re-run, score, validate no regressions

**NEVER skip steps 2-3.** Metadata-only analysis (tool counts, differentiation stats) leads to regressions. Every improvement must cite specific transcript content.

Repeat 3 times per loop before moving to the next loop.

## Loop Purposes

- **Loop 1 (Verifiability)**: Replace keyword checks with verifiable checks that catch false positives/negatives. Focus on tightening existing checks.
- **Loop 2 (Semantic Depth)**: Add checks that reward structural understanding, MCP tool usage, and cross-file connections.
- **Loop 3 (Weight Optimization)**: Tune scoring weights based on correlation between metrics and actual answer quality.

## Decision Authority

You have full authority to:
- Approve/reject verification script generation
- Generate new semantic check types
- Optimize scoring weights based on correlation
- Accept/reject improvements (automated safety checks prevent errors)

## Success Criteria

- Loop 1: Reduce false positives by 15% per iteration
- Loop 2: Increase MCP tool usage by 25%
- Loop 3: Achieve 0.2+ point separation between Sense and baseline

## Scoring Dimensions

The scorer measures 4 dimensions:
- **completeness** (0.40): Did the answer cover required content?
- **efficiency** (0.25): Token budget discipline
- **tool_fluency** (0.20): Did the AI use MCP vs grep fallback?
- **discoverability** (0.15): How much code did the AI surface?

## Tools Available

- `scripts/phases/phase1-run-analysis.sh` — Run scenarios, score, analyze
- `scripts/phases/phase2-run-improve.sh` — Apply improvements from improvements.json
- `scripts/phases/phase3-run-validate.sh` — Re-run, validate, rollback if regressed
- `scripts/tools/analyze-transcripts.py` — Automated pattern extraction
- `scripts/tools/generate-improvements.py` — Apply improvements to YAMLs
- `scripts/tools/validate-changes.py` — Safety checks and regression detection

## improvements.json Format

```json
{
  "scenarios": [
    {
      "repo": "gin",
      "modifications": [
        {
          "action": "tighten_check",
          "step_idx": 0,
          "old_check": {"type": "word", "value": "ServeHTTP"},
          "new_check": {"type": "contains", "value": "Engine.ServeHTTP"}
        },
        {
          "action": "add_check",
          "step_idx": 1,
          "new_check": {"type": "mcp_tool_used", "value": "sense_graph", "required": false}
        },
        {
          "action": "promote_to_required",
          "step_idx": 2,
          "check_type": "no_grep",
          "check_value": "no_grep"
        },
        {
          "action": "raise_threshold",
          "step_idx": 0,
          "check_type": "response_richness",
          "old_value": "5",
          "new_value": "8"
        },
        {
          "action": "update_weights",
          "weights": {"completeness": 0.35, "efficiency": 0.25, "tool_fluency": 0.25, "discoverability": 0.15}
        }
      ]
    }
  ]
}
```
