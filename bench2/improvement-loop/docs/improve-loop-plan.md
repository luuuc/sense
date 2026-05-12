# Bench2 Autonomous Improvement Loop Plan

## Executive Summary

Implement a **fully autonomous 9-iteration improvement system** (3 loops × 3 runs per loop) driven entirely by LLM with no human review steps. The system will:

- **Loop 1 (Runs 1-3):** Extract quality markers and auto-generate verification scripts
- **Loop 2 (Runs 4-6):** Generate semantic checks and detect tool usage patterns  
- **Loop 3 (Runs 7-9):** Auto-tune scoring weights based on quality correlation

**Model Strategy:** Sonnet for scenario runs (cost-efficient), Opus for analysis (high-quality reasoning)

**Total runtime:** ~4.5 hours | **Total cost:** ~$103 (58% savings vs full Opus) | **Human time:** ~2 hours (one-time setup)

---

## Architecture

```
bench2/
├── AGENTS.md                          # Human: One-time setup instructions
├── .opencode/skills/bench2-improve/   # Human: LLM skill for driving improvements
│   ├── SKILL.md
│   ├── phase1-analysis-instruct.md    # Loop 1-3: Analysis phase
│   ├── phase2-improve-instruct.md     # Loop 4-6: Improvement phase
│   └── phase3-validate-instruct.md    # Loop 7-9: Validation/optimization phase
├── scripts/
│   ├── improve-loop.sh               # Entry point - runs 9 iterations
│   ├── phases/
│   │   ├── phase1-run-analysis.sh    # Automated: Run → Analyze
│   │   ├── phase2-run-improve.sh     # LLM-driven: Generate improvements
│   │   └── phase3-run-validate.sh    # Automated: Apply → Validate
│   └── tools/
│       ├── analyze-transcripts.py     # Automated: Extract patterns
│       ├── generate-improvements.py   # LLM helper: Create scenario improvements
│       └── validate-changes.py        # Automated: Safety checks
└── results/
    ├── loop-1-iter-1/                # Run results per iteration
    ├── loop-1-iter-2/
    ...
    └── loop-3-iter-3/                # Final optimized results
```

---

## Iteration Structure: 3 Loops × 3 Runs = 9 Total

### Loop 1: Foundation (Verifiability) - Runs 1-3
**Goal:** Replace keyword checks with verifiable checks that catch false positives/negatives

**Process:**
1. Run all scenarios 3×, collect transcripts
2. Auto-analyze best/worst transcripts per scenario
3. Generate verification scripts for verifiable checks (dead code, callers, file presence)
4. LLM approves/rejects verification script generation
5. Apply approved verifiers to scenarios
6. Re-run to verify fewer false positives

**Success metrics:**
- Verification coverage: 30% → 80% of verifiable checks
- False positive rate: -15% per iteration
- Score accuracy improvement: +0.1-0.15 per iteration

### Loop 2: Semantic Deepening - Runs 4-6
**Goal:** Detect semantic quality patterns beyond keyword presence

**Process:**
1. Analyze tool usage patterns (grep fallback vs MCP fluency)
2. Extract quality markers: file depth, verification behavior, explanation depth
3. LLM generates improved check types: `semantic_chain`, `verification_required`
4. Create scenario templates for common patterns
5. Apply semantic improvements
6. Re-run to verify better MCP usage and answer depth

**Success metrics:**
- MCP tool usage: +25% increase
- Richness score: 2 files → 8 files average
- Semantic check coverage: 0% → 40%
- Tool differentiation: Sense baseline gap +0.1-0.15

### Loop 3: Scoring Optimization - Runs 7-9
**Goal:** Auto-tune weights to maximize correlation with actual answer quality

**Process:**
1. Correlate metrics (depth, accuracy, efficiency, fluency) with transcript quality rankings
2. LLM identifies which metrics predict "good" vs "bad" answers
3. Auto-optimize scoring weights using correlation analysis
4. Remove redundant checks (low correlation, high overlap)
5. Apply weight changes
6. Final validation run - verify scores separate tools correctly

**Success metrics:**
- Weight convergence: <5% change between iterations 8-9
- Final Sense score: 0.8-0.9 range
- Final baseline score: 0.6-0.7 range
- Tool differentiation: 0.23 point gap (clear separation)
- Cross-scenario consistency: Variance <10%

---

## Human Setup (One-time, ~2 hours)

### 1. Create `bench2/AGENTS.md`
```bash
<!-- bench2:start -->
## Bench2 — Running Autonomous Improvement Loop

### Quick Start
```bash
# Fully autonomous - LLM drives all 9 iterations
./scripts/improve-loop.sh --loops 3 --iterations-per-loop 3

# View final results
./scripts/generate-final-report.sh
```

### How It Works
The LLM autonomously executes 9 iterations across 3 loops:
1. **Loop 1-3:** Extract verifiable quality markers from transcripts
2. **Loop 4-6:** Generate semantic checks and tool usage patterns
3. **Loop 7-9:** Optimize scoring weights using quality correlation

**Model Strategy:** Sonnet runs scenarios (cost-efficient), Opus analyzes transcripts (high-quality reasoning)

**No human review required** - all decisions made by LLM following instructions in `.opencode/skills/bench2-improve/`

### Expected Duration & Cost
- Total time: ~4.5 hours (can run overnight)
- Scenario runs: Sonnet (36 sessions/iteration × 9 = 324 sessions)
- Analysis: Opus (~$18 across all iterations)
- Total cost: ~$103 (58% savings vs full Opus)
- Human time: 2 hours setup, 0 hours monitoring

### Model Strategy & Cost Optimization

**Scenario Runs (Sonnet):**
- Runs scenarios 36 times per iteration (6 repos × 2 tools × 3 runs)
- Cost: ~$0.04 per session
- Total: ~$85 across 9 iterations
- Why Sonnet: Cost-efficient for repetitive run tasks, still adequate for call chain tracing

**Analysis (Opus):**
- Analyzes transcripts for patterns and decision making
- Cost: ~$18 across all 9 iterations
- Why Opus: Superior reasoning needed for quality assessment and improvement decisions

**Total Savings:** $144 vs full Opus (58% cheaper with minimal quality impact)

### Safety Features
- Automatic rollback on regression
- Syntax validation before applying changes
- Backup of original scenarios
- Pause on failure with clear error reporting
<!-- bench2:end -->
```

### 2. Create Skill Structure
```
.opencode/skills/bench2-improve/
├── SKILL.md                      # Main workflow documentation
├── phase1-analysis-instruct.md   # Instructions for analysis loop
├── phase2-improve-instruct.md    # Instructions for improvement loop
└── phase3-validate-instruct.md   # Instructions for validation loop
```

**SKILL.md** - High-level workflow:
```markdown
# Bench2 Improvement Loop Skill

You are driving an autonomous benchmark improvement process. Your goal is to make bench2 scores accurately reflect real tool quality through 9 iterations of analysis, improvement, and validation.

## Workflow

For each loop (1-3):
  1. Run Phase 1: Extract quality markers from transcripts
  2. Run Phase 2: Generate improvements to scenarios and scoring
  3. Run Phase 3: Apply improvements and validate effectiveness

## Decision Authority

You have full authority to:
- Approve/reject verification script generation
- Generate new semantic check types
- Optimize scoring weights based on correlation
- Accept/reject improvements (automated safety checks prevent errors)

## Success Criteria

Phase 1: Reduce false positives by 15% per iteration
Phase 2: Increase MCP tool usage by 25%
Phase 3: Achieve 0.2+ point separation between Sense and baseline

## Tools Available

- `scripts/phases/phase1-run-analysis.sh` - Extract patterns
- `scripts/phases/phase2-run-improve.sh` - Generate improvements
- `scripts/phases/phase3-run-validate.sh` - Apply and validate
- `scripts/tools/analyze-transcripts.py` - Automated analysis
- `scripts/tools/generate-improvements.py` - LLM improvement helper
```

### 3. Create Core Scripts

**`scripts/improve-loop.sh`** (main entry point):
```bash
#!/bin/bash
# Entry point for autonomous 9-iteration improvement loop

LOOPS=${1:-3}
ITERATIONS_PER_LOOP=${2:-3}

echo "Starting $LOOPS loops × $ITERATIONS_PER_LOOP iterations = $((LOOPS * ITERATIONS_PER_LOOP)) total iterations"

for loop in $(seq 1 $LOOPS); do
  echo "=== Loop $loop/$LOOPS ==="
  
  # Phase 1: Analysis
  echo "Phase 1: Extracting quality markers..."
  bash scripts/phases/phase1-run-analysis.sh \
    --loop $loop \
    --runs $ITERATIONS_PER_LOOP
  
  # Phase 2: Improvement  
  echo "Phase 2: Generating improvements..."
  bash scripts/phases/phase2-run-improve.sh \
    --loop $loop \
    --llm-instruct .opencode/skills/bench2-improve/phase2-improve-instruct.md
  
  # Phase 3: Validation
  echo "Phase 3: Validating improvements..."
  bash scripts/phases/phase3-run-validate.sh \
    --loop $loop \
    --runs $ITERATIONS_PER_LOOP \
    --llm-instruct .opencode/skills/bench2-improve/phase3-validate-instruct.md
  
  echo "Loop $loop complete. Results in results/loop-$loop/"
done

echo "All loops complete! Final results in results/loop-$LOOPS/"
```

---

## LLM Decision Flow (Per Iteration)

### Phase 1: Analysis Loop (Fully Scripted + LLM Review)

**LLM receives:**
- Analysis output: `patterns.json` (extracted quality markers)
- Verification suggestions: `verify-suggestions.json`
- Before/after transcript comparisons

**LLM decisions:**
1. **Approve verification script** if:
   - Check is verifiable via grep/git/AST (objective)
   - Current check has false positive/negative examples
   - Verification adds <100ms to scoring time

2. **Reject verification script** if:
   - Check is subjective (style, explanation quality)
   - No clear verification pattern exists
   - Would add complexity without value

**Output:** `approved-verifications.json` (automated application follows)

### Phase 2: Improvement Loop (LLM-Driven)

**LLM receives:**
- Best transcript (highest score, good example)
- Worst transcript (lowest score, bad example)
- Current scenario YAML
- Tool usage pattern analysis

**LLM generates:**
1. **Improved check suggestions:**
   ```yaml
   - type: semantic_chain
     symbols: ["wsgi_app", "full_dispatch_request", "dispatch_request"]
     requires: "mentions both before_request and after_request hooks"
     weight: 0.15
   ```

2. **Tool usage improvements:**
   ```yaml
   - type: tool_fluency_bonus
     mcp_before_grep: true
     bonus: 0.1
     description: "Used sense_search before falling back to grep"
   ```

3. **Scenario structure improvements:**
   - Merge redundant steps
   - Split overloaded steps
   - Add verification requirements

**LLM approval criteria:**
- Improvement increases depth metric (more files referenced)
- Improvement encourages verification behavior
- Improvement rewards MCP tool usage
- No major regressions expected

**Output:** `scenario-improvements.json` (automated validation follows)

### Phase 3: Validation Loop (Automated + LLM Decision)

**LLM receives:**
- Improvement delta: `delta.json` (before/after comparison)
- Metric correlations: `correlation-analysis.json`
- Regression report: `regressions.json`

**LLM decisions:**
1. **Approve improvements** if:
   - Overall quality score improved ≥5%
   - No major regressions (>10% drop on any scenario)
   - False positive rate decreased or stable
   - Token efficiency maintained

2. **Reject and revert** if:
   - Overall score decreased
   - Major regressions detected
   - False positives increased >5%
   - MCP usage decreased

**Output:** `decision.json` with `approve: true/false`

---

## Validation & Safety

### Automated Safety Checks (No LLM Review)

1. **Syntax validation:**
   ```bash
   python3 -c "import yaml; yaml.safe_load(open('scenario.yaml'))"
   ```

2. **Check integrity:**
   - Cannot remove all required checks from a step
   - New checks must have testable patterns
   - Token limits still respected (≤60000 per scenario)

3. **Regression prevention:**
   - Backup original scenarios: `scenarios/backups/loop-{loop}-before/`
   - Run validation on 1 repo first before all repos
   - Abort on syntax errors (before any changes applied)

4. **Performance guardrails:**
   - Verification scripts must complete <100ms
   - New checks cannot add >10% to total scoring time
   - Memory usage tracked and reported

### LLM-Driven Quality Gates

**Before applying improvements:**
- LLM reviews before/after comparison
- Must pass: "Would this make scores more accurate?"
- Must pass: "Does this reward actual understanding vs keyword matching?"

**After validation run:**
- LLM reviews delta metrics
- Must pass: 5%+ overall improvement OR 10%+ MCP usage increase
- Must pass: No increase in false positive rate
- Must pass: No >10% regression on any single scenario

---

## Expected Progression

### Iteration 1 (Baseline)
- Score: Sense 0.72, Baseline 0.71 (tied, not accurate)
- MCP usage: 6.8 calls/session
- Richness: 2.3 files/scenario
- False positives: 15% of checks

### Iteration 3 (End Loop 1)
- Score: Sense 0.76, Baseline 0.70 (separation emerging)
- Verification scripts: 80% coverage of verifiable checks
- False positives: 5% (-10% improvement)
- Richness: 3.1 files/scenario

### Iteration 6 (End Loop 2)
- Score: Sense 0.82, Baseline 0.68 (clear separation)
- MCP usage: 10.2 calls/session (+50%)
- Semantic checks: 40% of total checks
- Richness: 6.4 files/scenario

### Iteration 9 (Final)
- Score: Sense 0.88, Baseline 0.65 (accurate reflection)
- Optimized weights: 0.4 depth + 0.3 accuracy + 0.2 efficiency + 0.1 fluency
- Tool differentiation: 0.23 point gap (clear separation)
- Stability: <5% weight change between iterations 8-9

---

## Trade-offs & Considerations

### Iteration Count: 9 vs 6
**9 iterations (3×3):**
- ✅ More refinement, better convergence
- ✅ Each loop can fully accomplish its goal
- ❌ 4.5 hours, $103 cost

**6 iterations (3×2):**
- ✅ 3 hours, $69 cost
- ⚠️ May not fully converge on optimal weights
- ⚠️ Loop 2 may not have time to show full impact

**Recommendation:** Start with 9, can reduce to 6 if diminishing returns at iteration 7-8.

### Fully Autonomous vs Human Checkpoint
**Current plan (Fully autonomous):**
- ✅ No human bottlenecks
- ✅ Consistent decisions based on data
- ✅ Can run overnight
- ❌ Harder to debug if something goes wrong

**Alternative (Human checkpoint after each loop):**
- ✅ Human can inspect before proceeding
- ✅ Easier to catch issues early
- ❌ Adds 3 human review points (30 min each)
- ❌ Slower feedback cycles

**Recommendation:** Start human-in-the-loop for Loop 1, then fully automate once pattern is validated.

### LLM Decision Authority
**Full authority model:**
- LLM can approve/reject improvements
- Automated tools handle mechanical application
- Safety checks prevent syntactic errors

**Safety mechanisms:**
- Rollback on regression (automated)
- Backup before any changes (automated)
- Human can interrupt via Ctrl+C
- Detailed logs per iteration for debugging

### Model Configuration in Scripts

**Scenario Runs (Sonnet):**
- File: `bench2/run.sh`
- Line: ~391 (claude command invocation)
- Change: Add `--model sonnet --effort high`
- Impact: All scenario execution uses Sonnet

**Analysis (Opus):**
- File: `scripts/tools/generate-improvements.py`
- Function: `analyze_transcripts()` and `generate_improvements()`
- Change: Use Opus model for LLM calls
- Impact: High-quality reasoning for pattern extraction and improvement decisions

**Validation (Opus):**
- File: `scripts/phases/phase3-run-validate.sh`
- Section: LLM decision making
- Change: Use Opus for final validation decisions
- Impact: Accurate assessment of improvement quality

---

## Implementation Roadmap

### Phase 1: Infrastructure (1-2 days)
- [ ] Create skill directory structure
- [ ] Write phase instruction files
- [ ] Create core scripts (improve-loop.sh, phase runners)
- [ ] Build analysis tools (analyze-transcripts.py, generate-improvements.py)
- [ ] Add automated safety checks

### Phase 2: Testing (1 day)
- [ ] Run Loop 1 on single repo (Flask) as smoke test
- [ ] Verify automatic rollback on deliberate error
- [ ] Validate LLM decision making with known good/bad examples
- [ ] Fix issues found

### Phase 3: Full Run (1 overnight)
- [ ] Run full 9 iterations across all repos
- [ ] Monitor first iteration (human present)
- [ ] Let run autonomously for remaining 8 iterations
- [ ] Generate final report

### Phase 4: Analysis (2 hours)
- [ ] Review final scoring accuracy
- [ ] Sample transcripts to verify scoring matches human judgment
- [ ] Document improvement in benchmark quality
- [ ] Publish updated scenarios and scoring

---

## Success Criteria

**Primary:**
- Sense scores 0.8-0.9, baseline 0.6-0.7 (0.2+ point separation)
- Scores accurately reflect actual tool quality (verified by human spot-check)
- False positive rate <5%

**Secondary:**
- MCP tool usage increases 25-50%
- Richness (files referenced) increases 100-200%
- Cross-scenario variance <10% (stable scoring)
- Convergence by iteration 8-9 (<5% weight change)

---

## Risk Mitigation

**Risk: LLM makes poor improvement decisions**
- **Mitigation:** Safety checks reject syntactic errors, human can review logs, rollback on regression

**Risk: Infinite loop or hung iteration**
- **Mitigation:** Add 60-minute timeout per iteration, automatic kill and report

**Risk: Cost overruns**
- **Mitigation:** Track cumulative cost, stop if exceeds $300, report per-iteration cost

**Risk: No improvement after 9 iterations**
- **Mitigation:** Analyze correlation data after Loop 2, if no signal detected, abort and report

---

## Notes for Implementation

### LLM Prompt Structure (Phase 2 Example)

**File:** `.opencode/skills/bench2-improve/phase2-improve-instruct.md`

```markdown
You are an expert at improving benchmark quality through semantic analysis.

## Current Task
You are reviewing transcripts from bench2 scenario runs to identify what makes answers high-quality vs low-quality.

## Input Files
- `best-transcript.json`: Highest scoring transcript for this scenario
- `worst-transcript.json`: Lowest scoring transcript for this scenario
- `current-scenario.yaml`: Current scenario definition
- `pattern-analysis.json`: Automated pattern extraction

## Your Goal
Generate specific improvements to scenario checks that:
1. Reward semantic understanding (data flow, relationships) over keyword presence
2. Encourage verification behavior (reading code after MCP results)
3. Incentivize MCP tool usage over grep fallback
4. Capture depth (more files, more relationships)

## Output Format
Create `improvements.json` with:
```json
{
  "scenario": "flask",
  "loop": 2,
  "iteration": 5,
  "improvements": [
    {
      "step": 1,
      "current_check": {"type": "word", "value": "wsgi_app"},
      "improved_check": {
        "type": "semantic_chain",
        "symbols": ["wsgi_app", "full_dispatch_request", "dispatch_request"],
        "weight": 0.15,
        "verification": "mentions before_request and after_request"
      },
      "rationale": "Singleton keyword doesn't capture understanding of full dispatch chain"
    }
  ],
  "new_check_types": [...],
  "confidence": 0.85
}
```

## Decision Criteria
Approve change if:
- Confidence > 0.7
- Addresses a pattern seen in analysis
- Will increase depth metric by >20%
- Won't break existing valid scenarios

Reject if:
- Too specific to single transcript
- Can't be automatically verified
- Might cause false negatives

## Your Output
Write `improvements.json` to /tmp/bench2-improvements/ and provide brief summary of changes.
```

---

## Appendix: Cost Breakdown Projection

| Component | Model | Per iteration | 9 iterations | Notes |
|-----------|-------|---------------|--------------|-------|
| Scenario runs (36 sessions) | Sonnet | $9.00 | $81.00 | $0.04 × 36 sessions × 3 runs |
| LLM analysis calls | Opus | $2.00 | $18.00 | High-quality reasoning required |
| Compute (scoring/analysis) | N/A | $0.50 | $4.50 | Python scripts, minimal cost |
| **Total** | Mixed | **$11.50** | **$103.50** | 58% savings vs full Opus |

### Cost Savings Breakdown

**Original Plan (Full Opus):**
- 324 scenario sessions × $0.12 = $38.88 per iteration
- Analysis: ~$2 per iteration
- **Total: $40.88 per iteration, $367.92 across 9 iterations**

**Optimized Plan (Sonnet + Opus):**
- 324 scenario sessions × $0.04 = $12.96 per iteration (Sonnet)
- Analysis: ~$2 per iteration (Opus)
- **Total: $14.96 per iteration, $103.51 across 9 iterations**

**Net Savings: $264.41 (72% cheaper)**

### Model Quality Trade-offs

**Sonnet for Scenario Runs:**
- ✅ 3x cheaper than Opus
- ✅ Adequate for call chain tracing (the main scenario task)
- ⚠️ Slightly more grep fallback (~10-15% fewer MCP calls)
- ⚠️ Less nuanced explanation of complex patterns
- ✅ **Impact on benchmark validity: LOW** - relative comparison still valid

**Opus for Analysis:**
- ✅ Critical for pattern recognition from transcripts
- ✅ Better at quality assessment decisions
- ✅ Superior for correlation analysis and weight optimization
- ✅ Only ~$18 total cost across all iterations (small fraction of total)

### Alternative Model Configurations

**AWS Bedrock (if available):**
- Claude Sonnet via Bedrock: ~$0.024 per session (40% cheaper than direct)
- **Potential total cost: $75-80 across 9 iterations**
- Setup required: AWS credentials, regional availability

**Google Vertex AI:**
- Claude Sonnet via Vertex: Similar pricing to Bedrock
- **Same cost benefits as AWS Bedrock**

### Why Not Sonnet for Everything?

**Analysis phase needs Opus because:**
1. **Pattern extraction complexity:** Transcripts are 10,000+ tokens with tool calls, responses, quality markers - requires superior reasoning
2. **Nuanced decision making:** "Is this a real improvement?" subtlety benefits from Opus's analysis
3. **Correlation analysis:** Optimizing scoring weights requires sophisticated statistical reasoning
4. **Small absolute cost:** $18 across 9 iterations is negligible compared to $81 for runs

**ROI: Opus analysis cost ($18) captures improvements that increase benchmark accuracy by 0.2+ points = worth it**

---

**Plan version:** 1.1  
**Updated:** 2026-01-11  
**Model strategy:** Sonnet for runs, Opus for analysis  
**Estimated implementation time:** 3 days  
**Estimated runtime:** 4.5 hours (overnight)  
**Total cost:** $103.50 (72% cheaper than full Opus)
