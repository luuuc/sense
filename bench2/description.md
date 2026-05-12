# bench2 — Scenario-Based Code Intelligence Evaluation

## Problem

`bench/` evaluates code intelligence tools with 10 isolated single-turn tasks scored against grep-generated ground truth. [An internal audit](../../bench/audit/gt-audit.md) found that **11 of 28 scored task ground-truths are broken** — inflated by orders of magnitude (dead-code/nextjs: 2,453 entries when tools find 0-99), wrong in methodology (blast-radius greps for string mentions, not structural references), or format-mismatched.

The result: **F1 scores of 0.0 from 5/7 tools on blast-radius/axum.** Running all combinations costs 358-490 Claude sessions with no useful differentiation.

## Design principles

1. **Human-mind, not grep-mind** — scenarios reflect what developers actually do: explore, trace, analyse, plan. Not single-turn Q&A against broken ground truth.

2. **Completeness × Efficiency** — the composite score. Code intelligence tools are enablers: they help Claude complete the same task faster, cheaper, with fewer tokens. They don't replace grep — they make grep unnecessary. The score reflects this.

3. **Grep/Read/MCP counts are data, not penalties** — reporting how Claude reached for tools tells you whether the intelligence tool was actually used. Zero grep + high MCP = the tool did its job.

4. **Machine-verifiable checklists** — every scenario has a structured checklist of expected findings. Scoring is transparent and explainable: each check is hit or miss, no cryptic normalization.

5. **Full-session, not single-turn** — Claude works through multiple steps in a single conversation. The transcript captures the complete workflow.

6. **Fewer sessions, more meaning** — 6 scenarios × 7 tools = 42 sessions vs. bench/'s 358+.

## Scoring

```
score = 0.6 × completeness + 0.4 × efficiency
```

- **Completeness** (60%): checklist hit rate. Required items = full credit, bonus items = 0.5 credit.
- **Efficiency** (40%): `1.0` if billed tokens ≤ 8,000, linearly scales to `0.0` at 60,000 tokens.

Additional metrics reported but **not part of the composite score**:

| Metric | Meaning |
|--------|---------|
| Grep count | How many times Claude used grep/Bash with grep |
| Read count | How many non-summary file reads Claude made |
| MCP count | How many MCP tool calls Claude made |
| Wall time | Session duration |
| Cost | USD spent on Claude API |
| Token split | Uncached input, output, cache read, cache write |

## Check types

| Type | Verification | Used for |
|------|-------------|----------|
| `contains` | Value appears in transcript (case-insensitive) | Fuzzy conceptual presence |
| `word` | Value appears as whole word (word boundary) | Symbol names, exact matching |
| `starts_with` | A transcript line starts with value | Method names in structured output |
| `mcp_tool_used` | Tool name appears in tool_calls | Was the right MCP tool invoked? |
| `no_grep` | grep was never used | Did the tool prevent raw grep? |
| `exact` | Value appears verbatim | Precise string matching |
| `diff_contains` | Value appears in `git diff` | File modification verification |

## Miss detection (supplementary)

Categorises bypasses into three types (not scored, purely diagnostic):

- **pre_mcp_misses**: grep/Read calls before any MCP tool invocation (discoverability problem)
- **post_mcp_verification_reads**: Read calls after MCP to source files (supplemental, good behavior)
- **post_mcp_misses**: grep calls after MCP (fallback after trying the tool)

## Comparison with bench/

| | bench/ | bench2/ |
|---|---|---|
| Ground truth | grep-generated (11/28 broken) | Human-curated checklists (machine-verifiable) |
| Composite score | F1 + fluency + discoverability + efficiency | Completeness × Efficiency |
| Grep/Read reporting | Penalised as "misses" | Reported as data (not penalised) |
| Sessions per run | 358-490 | 42 |
| Session type | Single prompt | Multi-step scenario (4 steps) |
| Token reporting | Input + output combined | Uncached/cached split |
| Face validity | Low (0.0 F1 on blast-radius) | High (real developer work) |
| Explainable | No (cryptic normalization) | Yes (per-check hit/miss) |
