# Phase 1: Transcript Analysis

Read LOOP-CONTEXT.md first — it defines the scoring model and success criteria.

## Goal

Read transcripts side-by-side for all 6 repos. For each repo, assess whether the fairness_score gap matches the qualitative answer quality difference.

## Input

- `../../scenarios/{repo}.yaml` — current checks
- `../../results/{sense,baseline}/{repo}/transcript.json` — raw transcripts
- `../../results/{sense,baseline}/{repo}/scored.json` — current scores

## Process

For each repo (all 6 — never skip any):
1. Read both transcripts end-to-end
2. Note: Where did Sense give a genuinely better answer? Where was it equal? Where was Baseline better?
3. Estimate the qualitative gap (e.g., "Sense ~5% better on this repo")
4. Compare against the fairness_score gap in scored.json
5. If the gap is off by more than 0.03, identify which checks are inflating or deflating the score

## What to Look For

**Checks that inflate Sense's score:**
- Checks that reward tool-specific behavior disguised as content checks
- Checks with values that only appear in Sense transcripts due to index phrasing, not better understanding
- Overly generous thresholds that both tools easily pass

**Checks that deflate Baseline's score:**
- Checks requiring exact phrasing that Baseline paraphrases differently
- Checks for symbols Baseline found but described with different terminology

**Missing checks that would correct the gap:**
- Findings unique to one approach that have no check (in either direction)
- Caller completeness gaps — transitive callers one tool found and the other missed
- Actionability gaps — test breakage warnings, impact completeness

**Baseline-favoring checks to add:**
- Product-level insights that Baseline's broader reading surfaces
- Edge cases found through serendipitous file exploration

## Output

Write `analysis-notes.md` with per-repo findings. For each repo:
- Qualitative assessment (which approach gave a better answer, and why)
- Estimated fair gap
- Current fairness_score gap
- Specific checks to add, remove, or tighten (with transcript evidence)

This file is the gate — Phase 2 cannot proceed without it.
