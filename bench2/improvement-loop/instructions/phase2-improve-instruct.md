# Phase 2: Improvement Instructions — Semantic Depth

You are reviewing transcripts to generate semantic improvements that reward deeper understanding.

## Input Files
- Best transcript (highest score) per scenario
- Worst transcript (lowest score) per scenario
- Current scenario YAML
- `results/loop-N/analysis.json` — pattern analysis

## Your Goal

Generate specific improvements to scenario checks that:
1. Reward semantic understanding (data flow, relationships) over keyword presence
2. Encourage verification behavior (reading code after MCP results)
3. Incentivize MCP tool usage over grep fallback
4. Capture depth (more files, more relationships)

## Improvement Types

### Semantic chain checks
Replace single-word checks with checks that require demonstrating a connection:
```yaml
- type: contains
  value: "wsgi_app calls full_dispatch_request"
  description: Demonstrates understanding of the dispatch chain
```

### Tool usage checks
Add checks that reward structural tool usage:
```yaml
- type: mcp_tool_used
  value: sense_graph
  required: false
  description: Used structural graph query for caller analysis
```

### Verification depth
Add or raise response_richness thresholds:
```yaml
- type: response_richness
  value: '8'
  required: true
  description: Referenced 8+ unique source files
```

## Decision Criteria

**Approve change** if:
- Confidence > 0.7
- Addresses a pattern seen in analysis
- Will increase depth metric by >20%
- Won't break existing valid scenarios

**Reject** if:
- Too specific to single transcript
- Can't be automatically verified
- Might cause false negatives for both tools equally

## Output

Write `improvements.json` to `results/loop-N-iter-M/improvements.json`.

Focus on:
- Adding 2-3 semantic chain checks per scenario
- Adding 1-2 mcp_tool_used checks per scenario where sense used MCP
- Promoting existing no_grep bonus checks to required
- Keeping changes incremental — no more than 40% of checks modified
