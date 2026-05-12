# Bench2 Implementation Checklist

Based on the autonomous improvement loop plan, here's your step-by-step implementation guide for the next session.

---

## Phase 1: Infrastructure (1-2 hours of focused work)

### Step 1: Create Skill Structure (10 minutes)
```bash
cd /Users/luc/Developer/luuuc/oss/sense/bench2
mkdir -p .opencode/skills/bench2-improve
```

### Step 2: Write Skill Files (30 minutes)
Create these 4 files in `.opencode/skills/bench2-improve/`:

**1. SKILL.md** (Main workflow documentation)
- Copy content from improve-loop-plan.md lines 145-179
- Defines overall workflow and decision authority

**2. phase1-analysis-instruct.md** (Analysis loop instructions)
- Define LLM instructions for Phase 1
- Focus: Extract quality markers, suggest verification scripts
- Decision criteria: Verifiable vs subjective checks

**3. phase2-improve-instruct.md** (Improvement loop instructions)
- Define LLM instructions for Phase 2
- Focus: Compare best/worst transcripts, generate improvements
- Decision criteria: Pattern generalization, semantic depth

**4. phase3-validate-instruct.md** (Validation loop instructions)
- Define LLM instructions for Phase 3
- Focus: Review delta metrics, approve/reject improvements
- Decision criteria: 5% improvement, no major regressions

### Step 3: Create Scripts Directory (5 minutes)
```bash
mkdir -p scripts/phases
mkdir -p scripts/tools
```

### Step 4: Write Core Scripts (45 minutes)

**1. scripts/improve-loop.sh** (Main orchestrator)
- Copy from improve-loop-plan.md lines 183-219
- Entry point that runs 3 loops × 3 iterations

**2. scripts/phases/phase1-run-analysis.sh** (Analysis phase)
- Orchestrates: run → analyze → suggest → review → apply
- Run scenarios with Sonnet
- Call analyze-transcripts.py
- Generate verification suggestions
- LLM review with phase1-instruct.md
- Apply approved verifiers

**3. scripts/phases/phase2-run-improve.sh** (Improvement phase)
- Orchestrates: find transcripts → analyze → generate → validate → apply
- Find best/worst transcripts per scenario
- LLM generate improvements with phase2-instruct.md
- Call generate-improvements.py
- Apply validated improvements

**4. scripts/phases/phase3-run-validate.sh** (Validation phase)
- Orchestrates: re-run → measure → decide → apply/revert
- Re-run scenarios with improvements
- Measure delta metrics
- LLM validation decision with phase3-instruct.md
- Apply or revert based on decision

### Step 5: Build Python Tools (30 minutes)

**1. scripts/tools/analyze-transcripts.py** (Pattern extraction)
- Input: transcript.json files from results/
- Output: patterns.json with quality markers
- Extracts: file depth, verification behavior, tool usage, richness

**2. scripts/tools/generate-improvements.py** (LLM improvement helper)
- Input: best/worst transcripts, current scenario
- Output: improvements.json with check suggestions
- Helper functions for LLM-driven improvement generation

**3. scripts/tools/validate-changes.py** (Safety checks)
- Validate YAML syntax
- Check for check integrity (not removing all required checks)
- Backup scenarios before changes
- Regression detection
- Performance guardrails (verification <100ms)

---

## Phase 2: Testing (30 minutes)

### Step 6: Smoke Test (30 minutes)
```bash
# Test on single repo (Flask)
cd bench2
bash scripts/improve-loop.sh --loops 1 --iterations-per-loop 2 --repo flask

# Verify:
echo "=== Verifying Sonnet usage ==="
grep -i '"model": "sonnet"' results/loop-1/flask/*/transcript.json | wc -l
echo "Should show 6 transcripts (2 tools × 3 runs)"

echo "=== Verifying Opus usage in analysis ==="
grep -i '"model": "opus"' results/loop-1/analysis.log | wc -l
echo "Should show analysis calls using Opus"

echo "=== Checking for syntax errors ==="
python3 -c "import yaml; yaml.safe_load(open('scenarios/flask.yaml'))"
echo "✓ No syntax errors"

echo "=== Checking backups created ==="
ls -la scenarios/backups/loop-1-before/
echo "Should see backup of original flask.yaml"

echo "=== Checking improvements.json generated ==="
ls -la results/loop-1/improvements.json
echo "Should exist"
```

**What to verify:**
- Sonnet is used for scenario runs (check transcripts)
- Opus is used for analysis (check logs)
- No syntax errors
- Backups created
- improvements.json generated
- No major regressions

---

## Phase 3: Full Run (Overnight, 1 command)

### Step 7: Start Full 9-Iteration Run
```bash
cd bench2
bash scripts/improve-loop.sh --loops 3 --iterations-per-loop 3
```

**What happens:**
- Runs autonomously for 4.5 hours
- Creates results/loop-{1,2,3}/ directories
- Generates improvement logs each iteration
- Auto-validates each loop before proceeding
- Stops on error with clear message

**When to start:** Before bed, after dinner, or when leaving for 5+ hours

**Check in after:** 30 minutes (to verify loop 1 started), then let it run

---

## Phase 4: Next Morning Analysis (30 minutes)

### Step 8: Review Results
```bash
cd bench2

# Generate final comparison report
echo "=== Generating final report ==="
bash report.sh --md > results/final-improvement-report.md
echo "✓ Report saved to results/final-improvement-report.md"

# Check total cost
echo "=== Cost breakdown ==="
cat results/cost-summary.json | python3 -m json.tool

# Check final scores
echo "=== Final Sense scores ==="
jq -r '.overall_score' results/loop-3/sense/*/scored.json | sort -nr
echo "Expected: 0.8-0.9 range"

echo "=== Final baseline scores ==="
jq -r '.overall_score' results/loop-3/baseline/*/scored.json | sort -nr
echo "Expected: 0.6-0.7 range"

# Check separation
echo "=== Tool differentiation ==="
sense_avg=$(jq -r '.overall_score' results/loop-3/sense/*/scored.json | awk '{sum+=$1} END {print sum/NR}')
baseline_avg=$(jq -r '.overall_score' results/loop-3/baseline/*/scored.json | awk '{sum+=$1} END {print sum/NR}')
echo "Sense average: $sense_avg"
echo "Baseline average: $baseline_avg"
echo "Gap: $(echo "$sense_avg - $baseline_avg" | bc)"
echo "Expected: 0.2+ point separation"

# Spot-check 2-3 transcripts
echo "=== Sampling transcripts ==="
ls results/loop-3/sense/flask/transcript.json | head -1
ls results/loop-3/baseline/flask/transcript.json | head -1
```

**Success criteria:**
- **Sense**: 0.8-0.9 score
- **Baseline**: 0.6-0.7 score
- **Gap**: 0.2+ points (clear separation)
- **Cost**: ~$103 total
- **MCP usage**: Noticeable increase iteration-over-iteration
- **Quality**: Read 1-2 transcripts to verify scoring matches human judgment

If all criteria met: **Mission accomplished!** ✅

---

## Expected Timeline

**Infrastructure session:** 1.5-2 hours (build everything)
**Testing session:** 30 minutes (verify on Flask)
**Full run:** 4.5 hours (overnight, unattended)
**Analysis session:** 30 minutes (review results)

**Total human time:** 3 hours over 2-3 sessions
**Total compute time:** 5 hours (includes scoring/analysis)

---

## Quick Reference

### Files to Create (from plan)
Copy these directly from improve-loop-plan.md:
- `bench2/.opencode/skills/bench2-improve/SKILL.md` (lines 145-179)
- `bench2/scripts/improve-loop.sh` (lines 183-219)

### Scripts to Write (use plan as spec)
Create these based on plan requirements:
- `bench2/scripts/phases/phase1-run-analysis.sh`
- `bench2/scripts/phases/phase2-run-improve.sh`
- `bench2/scripts/phases/phase3-run-validate.sh`
- `bench2/scripts/tools/analyze-transcripts.py`
- `bench2/scripts/tools/generate-improvements.py`
- `bench2/scripts/tools/validate-changes.py`

### Model Changes to Make
**Scenario runs (Sonnet) - in run.sh:**
```bash
# Around line 391
claude --model sonnet --effort high "${claude_args[@]}" > "$result_dir/transcript.json"
```

**Analysis (Opus) - in Python scripts:**
```python
# In generate-improvements.py and validate-changes.py
llm_call(model="opus", prompt=analysis_prompt, ...)
```

---

## Troubleshooting Checklist

If something breaks during implementation:

**Scenario runs fail:**
- Check: `run.sh` has `--model sonnet`
- Check: Anthropic API key in env or ~/.claude
- Test: `claude --model sonnet --print "test"`

**Analysis fails:**
- Check: Python scripts specify `model="opus"`
- Check: API key has Opus access
- Test: `claude --model opus --print "test"`

**Syntax errors in scenarios:**
- Run: `python3 -c "import yaml; yaml.safe_load(open('scenarios/flask.yaml'))"`
- Check: Backup files in `scenarios/backups/`

**No improvements detected:**
- Check: Are transcripts being parsed correctly?
- Check: LLM instructions loaded correctly in phase scripts
- Check: improvements.json being generated and used

**Cost too high:**
- Verify Sonnet is being used: Check transcript.json for model field
- Check cost tracking in results/cost-summary.json
- Consider: Reduce to 6 iterations (--loops 2 --iterations-per-loop 3)

---

## Success Metrics Post-Implementation

**Quantitative:**
- [ ] All 9 iterations completed without errors
- [ ] Sense scores in 0.8-0.9 range
- [ ] Baseline scores in 0.6-0.7 range
- [ ] 0.2+ point separation between tools
- [ ] Total cost ~$103 (within 10% of estimate)
- [ ] MCP usage increased 25-50% iteration-over-iteration

**Qualitative:**
- [ ] Read 2-3 final transcripts: Scoring matches human judgment
- [ ] False positive rate <5% (after loop 1 improvements)
- [ ] Benchmark accurately separates shallow from deep analysis
- [ ] Can distinguish Sense from baseline (not tied scores)

---

**Checklist version:** 1.0  
**Based on plan:** bench2/improve-loop-plan.md v1.1  
**Estimated total effort:** 3 hours human time + 4.5 hours compute time  
**Expected outcome:** Autonomous benchmark that accurately reflects tool quality
