# Loop 3 Iter 1: Weight Optimization Analysis

## Current weights
completeness=0.40, efficiency=0.25, tool_fluency=0.20, discoverability=0.15

## Fresh Phase 1 scores (this run)

| Repo | Sense | Baseline | Gap | Sense tools |
|------|-------|----------|-----|-------------|
| axum | 0.9148 | 0.6718 | +0.2430 | 8 MCP, 0 grep |
| discourse | 0.8196 | 0.6854 | +0.1342 | 7 MCP, 9 grep |
| flask | 0.7655 | 0.6401 | +0.1254 | 4 MCP, 4 grep |
| gin | 0.8916 | 0.6242 | +0.2674 | 6 MCP, 0 grep |
| javalin | 0.9586 | 0.8217 | +0.1369 | 10 MCP, 0 grep |
| nextjs | 0.9798 | 0.6788 | +0.3010 | 12 MCP, 0 grep |
| **Average** | **0.8883** | **0.6870** | **+0.2013** | |

## Per-dimension gap analysis

| Repo | completeness | efficiency | tool_fluency | discoverability |
|------|-------------|-----------|-------------|----------------|
| axum | +0.084 | -0.022 | +1.000 | +0.100 |
| discourse | +0.122 | -0.008 | +0.438 | +0.000 |
| flask | +0.064 | +0.000 | +0.500 | +0.000 |
| gin | +0.151 | +0.029 | +1.000 | +0.000 |
| javalin | +0.196 | -0.165 | +0.500 | +0.000 |
| nextjs | +0.125 | +0.205 | +1.000 | +0.000 |
| **Average** | **+0.123** | **+0.006** | **+0.740** | **+0.017** |

## Weighted contribution to overall gap

| Dimension | Avg gap | Weight | Contribution | % of total |
|-----------|---------|--------|-------------|-----------|
| completeness | +0.123 | 0.40 | +0.049 | 24.5% |
| efficiency | +0.006 | 0.25 | +0.002 | 0.8% |
| tool_fluency | +0.740 | 0.20 | +0.148 | 73.4% |
| discoverability | +0.017 | 0.15 | +0.003 | 1.2% |

## Per-repo transcript analysis

### axum
- **Sense**: 8 MCP calls (sense_graph x2, sense_search x5, sense_status), 0 grep. Full MCP workflow. Scored 1.0 on fluency, 1.0 on discoverability.
- **Baseline**: 3 grep, 16 Read. grep+Read workflow. Scored 0.0 on fluency, 0.9 on discoverability.
- **Score reflects quality**: Yes. Sense used structural tools effectively; baseline relied on grep+Read.
- **Anomaly**: efficiency -0.022 (baseline slightly more efficient). This is because MCP calls add token overhead. Not a scoring issue — accurate reflection.

### discourse
- **Sense**: 7 MCP + 9 grep. Mixed approach — used MCP for graph/search/blast but fell back to grep frequently.
- **Baseline**: 24 grep, 18 Read. Heavy grep usage.
- **Score reflects quality**: Partially. Sense tool_fluency only 0.4375 (= 7/(7+9)) despite using MCP effectively. The grep fallback penalty is appropriate — sense should use MCP more consistently.
- **Anomaly**: discoverability both 1.0. Both tools referenced enough files. No differentiation.

### flask
- **Sense**: 4 MCP + 4 grep. Equal MCP/grep split → fluency 0.5.
- **Baseline**: 6 grep, 5 Read. Pure grep.
- **Score reflects quality**: Yes. Flask is a small codebase where grep is nearly as effective as MCP. The 0.5 fluency is fair.
- **Anomaly**: discoverability both 0.5 — same number of files referenced by both.

### gin
- **Sense**: 6 MCP, 0 grep. Pure MCP → fluency 1.0.
- **Baseline**: 6 grep, 9 Read. Pure grep → fluency 0.0.
- **Score reflects quality**: Yes. Maximum differentiation on tool_fluency.
- **Anomaly**: discoverability both 0.7 — same. Previous run had sense at 0.4 and baseline at 0.7; now equalized.

### javalin
- **Sense**: 10 MCP, 0 grep. Pure MCP.
- **Baseline**: 0 grep, 18 Read. Read-only → fluency defaults to 0.5 (no grep or MCP).
- **Score reflects quality**: Yes, but baseline getting 0.5 fluency by default (no grep used) reduces the gap from what should be a 1.0 vs 0.0 scenario.
- **Anomaly**: efficiency -0.165 (baseline is significantly more efficient). Baseline used 18 Read calls with less token overhead than MCP sessions. This is a real pattern — MCP adds overhead.
- **Score inflation**: baseline 0.5 tool_fluency is somewhat inflated. With 0 MCP and 0 grep, it gets the neutral default rather than being penalized for not using MCP.

### nextjs
- **Sense**: 12 MCP, 0 grep. Pure MCP, very efficient (only 2 Read calls after MCP).
- **Baseline**: 24 grep, 26 Read. Heavy grep+Read.
- **Score reflects quality**: Yes. Sense was both more effective and more efficient on this large codebase.
- **Anomaly**: discoverability both 1.0 — no differentiation despite very different approaches.

## Cross-scenario consistency

**tool_fluency differentiates consistently**: Gap ranges from +0.438 (discourse) to +1.0 (axum, gin, nextjs). It's the most reliable differentiator across ALL repos. The lower gaps come from sense using grep fallback (discourse: 9 greps, flask: 4 greps) or baseline not using grep (javalin: 0.5 default).

**completeness differentiates moderately**: Gap ranges from +0.064 (flask) to +0.196 (javalin). Always positive but modest.

**efficiency is noise**: Gap ranges from -0.165 (javalin) to +0.205 (nextjs). Direction varies by repo — not a reliable differentiator. Sometimes MCP is more efficient (nextjs), sometimes Read-only is (javalin).

**discoverability is flat**: Gap is 0 on 4/6 repos. Both tools reference similar numbers of files.

## Proposed weight changes

**Rationale**: tool_fluency is 73.4% of the gap but gets only 20% weight. Efficiency is 0.8% of the gap but gets 25% weight. This is the primary imbalance.

| Dimension | Current | Proposed | Change | Reason |
|-----------|---------|----------|--------|--------|
| completeness | 0.40 | 0.35 | -0.05 | Moderate differentiator, slight overweight |
| efficiency | 0.25 | 0.20 | -0.05 | Near-zero differentiation, noise |
| tool_fluency | 0.20 | 0.25 | +0.05 | Dominant differentiator, massively underweight |
| discoverability | 0.15 | 0.20 | +0.05 | Slight increase to reward file breadth variance |

Sum: 0.35 + 0.20 + 0.25 + 0.20 = 1.00 ✓
All weights in [0.10, 0.45] ✓
All changes ≤ 0.05 ✓

**Projected impact**: With new weights, the average gap would increase from ~0.20 to ~0.22 due to higher weight on the most differentiating dimension.

## Additional improvements

### gin step 0: response_richness threshold
The previous run showed sense referencing only 4 unique files vs baseline's 7. The richness threshold of 7 penalizes sense's focused approach in a small codebase. Consider lowering to 5.

### javalin baseline tool_fluency default
Baseline gets 0.5 fluency by defaulting when it uses neither grep nor MCP. This inflates baseline's score. This is a scorer.py issue, not a scenario issue — flagging for awareness but not changing weights to compensate.
