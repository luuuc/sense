# Claude Code + Sense Adoption Analysis

Analysis of why Claude Code skips Sense tools when it can answer from existing context, and recommendations to improve adoption.

## What Sense Already Does Well

1. **Hooks in `settings.json`** — SessionStart, PreToolUse (Grep/Glob/Agent/Bash), PostToolUse (Write/Edit), PreCompact, SubagentStart
2. **Auto-permissions** — `mcp__sense__*` allowed in `settings.json`
3. **Agent blocking** — PreToolUse hook *denies* Agent/Explore subagents and suggests Sense tools instead
4. **Bash nudging** — PreToolUse on Bash emits `additionalContext` suggesting Sense (but doesn't block)
5. **CLAUDE.md instructions** — large section with tool table, workflows, and "MUST" language
6. **MCP server instructions** — injected via system-reminder

## Why Claude Still Skipped Sense

The problem is clear: **nothing forced Claude to use Sense before responding to a pure text question.** No tool was called that the hooks intercept — Claude just answered from CLAUDE.md context. The hooks only fire on tool use, and the SessionStart hook tells Claude to load tools but doesn't block anything.

## Available Hook Response Types

Claude Code PreToolUse hooks support these response strategies:

| Response | UX | Behavior |
|----------|-----|----------|
| `deny` | Red block | Blocks tool call. Disruptive — user sees "Sense" in a red wall |
| `ask` | Yellow prompt | User must confirm. Too much friction for routine calls |
| `additionalContext` | Invisible to user | Claude sees it, user doesn't. Current grep behavior |
| **`systemMessage`** | **Non-blocking note** | **Shown to user as a gentle warning, tool still runs** |

**`systemMessage` is the sweet spot** — Claude gets the nudge, the user sees a brief non-blocking note (not a red wall), and the tool still executes. Sense looks helpful, not obstructive.

Example hook response:
```json
{
  "additionalContext": "Sense has this indexed — try sense_search or sense_graph instead of grep for structural queries.",
  "systemMessage": "Tip: Sense can answer this faster via sense_search."
}
```

This dual approach gives Claude the full context (`additionalContext`) while showing the user a brief, friendly tip (`systemMessage`).

## Recommendations (Most Impactful First)

### 1. The `summary.md` cold-start is the weakest link

The CLAUDE.md says "read `.sense/summary.md` as your FIRST action" — but this relies entirely on the model obeying a long instruction buried in a large CLAUDE.md. The SessionStart hook reinforces it, but session-start output competes with a lot of other context at conversation start.

**Suggestion**: The SessionStart hook could inject the summary.md content directly as part of its output, rather than telling Claude to read it. If the summary is already in context, Claude doesn't need to remember to read it — it's just there. Something like:

```json
{"message": "...", "summary": "<contents of .sense/summary.md>"}
```

This removes one round-trip and eliminates the "forgot to read it" failure mode.

### 2. Replace `deny` with `systemMessage` + `additionalContext` for grep and subagents

Current behavior:
- **Agent/Explore**: denied (red block) — bad UX, makes Sense look like an obstacle
- **Bash grep**: `additionalContext` only — Claude sees it, often ignores it
- **Grep tool**: `{}` (no-op) — completely ignored

**Suggestion**: Use `systemMessage` + `additionalContext` for all three. This provides:
- A visible but non-blocking tip to the user ("Tip: Sense can answer this faster")
- Full context to Claude about what Sense tool to use instead
- No red blocks, no friction — Sense looks like a helpful assistant, not a gatekeeper

For **Agent/Explore subagents** specifically: switch from `deny` to `systemMessage` + `additionalContext`. The deny is the most user-visible pain point. With a `systemMessage`, the user sees "Tip: Sense has this codebase indexed — the AI can use sense_search/sense_graph instead of spawning an explore agent" and Claude gets the `additionalContext` nudge. If Claude still spawns the agent, the user at least saw the tip and can redirect.

For **Bash grep/find** and **Grep tool**: upgrade from `additionalContext`/no-op to `systemMessage` + `additionalContext`. Same pattern — gentle visible tip + Claude context.

### 3. The Grep tool hook returns `{}` (no-op)

`Grep` is in the PreToolUse matcher (`"Grep|Glob|Agent|Bash"`), but the hook returns empty `{}` for Grep input. This means the Grep tool is matched but not actually nudged.

**Suggestion**: Apply the `systemMessage` + `additionalContext` pattern here too. No reason to match Grep in the hook if the response is empty.

### 4. CLAUDE.md instructions are project-specific, not portable

The large Sense section in CLAUDE.md only exists because this project's maintainer wrote it. For Sense to work well "on any computer," `sense setup` should offer to inject a standardized CLAUDE.md snippet — which it may already do. But the snippet could be shorter and more targeted. The current one is ~50 lines; a 10-line version with the same "MUST" rules would get read more reliably.

### 5. No slash command / skill for Sense

There's no `/sense` skill in `.claude/commands/`. A simple skill that runs the orientation workflow (read summary, load tools, call status + conventions) would give users a one-command way to orient, and would also serve as a template Claude sees in its available skills list — a constant reminder that Sense exists.

**Suggestion**: `sense setup` could generate a `.claude/commands/sense-orient.md` skill.

### 6. Subagents don't get Sense context

The SubagentStart hook fires, but subagents start fresh — they don't have the Sense tools loaded or the CLAUDE.md context about using Sense. The current Agent denial is a blunt fix. With `systemMessage` instead of `deny`, subagents would still spawn but the user would see the tip, and `additionalContext` would give Claude a reason to prefer Sense next time.

Longer term: injecting Sense tool availability into subagent prompts automatically via the SubagentStart hook would be the ideal solution.

## Implementation Plan

### Phase 1 — Quick wins (hours, not days)

**1a. Switch Agent/Explore from `deny` to `systemMessage` + `additionalContext`**
- Effort: Change one conditional in `hook pre-tool-use`
- Impact: Immediate UX improvement — removes the most visible pain point
- Risk: Claude might ignore the nudge and spawn agents anyway. Acceptable — user saw the tip.

**1b. Fix Grep/Glob hook returning `{}`**
- Effort: Same code path as 1a, just add the response
- Impact: Closes a gap where a matched tool gets zero guidance
- Ship together with 1a.

**1c. Upgrade Bash grep/find from `additionalContext` to `systemMessage` + `additionalContext`**
- Effort: Add one field to the existing response
- Impact: User now sees the tip too, not just Claude. Makes Sense more discoverable.

**Ship as one PR: 1a + 1b + 1c**

### Phase 2 — High-impact, moderate effort (a day or two)

**2a. SessionStart injects `summary.md` content directly**
- Effort: Read the file in the hook, embed it in the response
- Impact: **This is the single highest-impact change.** Eliminates the entire "forgot to read summary.md" failure class. Claude starts every conversation with the codebase map already in context.
- Consideration: Keep it concise — if summary.md is too large it'll compete with other session-start context. Current one is 40 lines, that's fine.

**2b. Shorten the CLAUDE.md snippet that `sense setup` injects**
- Effort: Rewrite the template from ~50 lines to ~10-15
- Impact: Shorter instructions get read more reliably. With Phase 2a doing the heavy lifting, CLAUDE.md just needs the "use Sense tools for X, Y, Z" rules, not the full cold-start protocol.

**Ship as one PR: 2a + 2b**

### Phase 3 — Subagent & skill generation (a few days)

**3a. Generate a `/sense-orient` slash command via `sense setup`**
- Effort: Write a `.claude/commands/sense-orient.md` template
- Impact: Gives users a manual trigger, and its presence in the skills list reminds Claude that Sense exists. Lower priority because Phase 2a solves the automatic case.

**3b. Generate a `deep-explore` subagent via `sense setup` (default, no flag)**
- Effort: Write a `.claude/agents/deep-explore.md` template, generate it as part of standard `sense setup`
- Impact: Self-contained exploration agent — parent just spawns it, no need to know how Sense works. Massive token savings.
- **No opt-in flag needed.** The file is tiny, has zero runtime cost if never spawned, and its presence in `.claude/agents/` makes Claude aware Sense exists every time it considers spawning an agent.

**3c. SubagentStart hook injects Sense context into non-Sense subagents — CONFIRMED FEASIBLE**
- Complements 3b: the `deep-explore` agent is purpose-built, but other agents (Explore, general-purpose, custom) also benefit from Sense awareness via `additionalContext` injection.

**Ship together: 3a + 3b + 3c as one PR**

---

## Deep Dive: Sense Subagent Strategy

### Prior Art: grepai's `deep-explore` Agent

grepai ships a `deep-explore.md` subagent (via `grepai agent-setup --with-subagent`). It wraps `grepai search` and `grepai trace` via Bash into a self-contained agent:

```markdown
---
name: deep-explore
description: Deep codebase exploration using grepai semantic search
  and call graph tracing.
tools: Read, Grep, Glob, Bash
---
## Instructions
You are a specialized code exploration agent with access to grepai...
```

Key insight: **the parent doesn't need to know how grepai works — it just spawns the agent.** The agent file contains full instructions, tool list, and workflow. This is the right UX pattern.

### What Sense Should Generate: `deep-explore.md`

Generated by default as part of `sense setup` (no `--with-subagent` flag needed):

```markdown
---
name: deep-explore
description: Deep codebase exploration using Sense index. Symbol
  relationships, semantic search, impact analysis, conventions.
tools: Read, Bash
model: inherit
---

## Instructions

You are a codebase exploration agent with access to Sense MCP tools.

### First Action

Load Sense tools:
ToolSearch("select:mcp__sense__sense_graph,mcp__sense__sense_search,
mcp__sense__sense_blast,mcp__sense__sense_conventions,
mcp__sense__sense_status")

### Tools

| Question | Tool |
|---|---|
| Who calls X? What does X call? | `sense_graph symbol="X"` |
| Find code related to a concept | `sense_search query="..."` |
| What breaks if I change X? | `sense_blast symbol="X"` |
| What patterns exist? | `sense_conventions` |

### Workflow

1. Load Sense tools (ToolSearch — one call)
2. Use sense_search for broad exploration
3. Use sense_graph to trace relationships
4. Use Read only to examine specific file contents
5. Synthesize findings into a clear summary
```

**Advantage over grepai's approach**: Sense uses MCP tools (structured, typed responses) instead of Bash CLI calls (parsing JSON output). The subagent gets rich structured data directly.

### SubagentStart Hook: Sense-Awareness for All Subagents

The `deep-explore` agent is purpose-built, but other subagents (Explore, general-purpose, custom agents) also benefit. The SubagentStart hook handles these.

The hook receives:
```json
{
  "hook_event_name": "SubagentStart",
  "agent_type": "Explore",
  "agent_id": "agent-abc123",
  "cwd": "/Users/..."
}
```

And injects `additionalContext` into the subagent's context at startup:
```json
{
  "hookSpecificOutput": {
    "hookEventName": "SubagentStart",
    "additionalContext": "This project has a Sense index (6708 symbols, 15261 edges).\n\nBefore using grep/find/file-walking, load Sense tools:\nToolSearch(\"select:mcp__sense__sense_graph,mcp__sense__sense_search,mcp__sense__sense_blast,mcp__sense__sense_conventions,mcp__sense__sense_status\")\n\n- Who calls X? What does X depend on? → sense_graph\n- Find code related to a concept → sense_search\n- What breaks if I change X? → sense_blast\n- What patterns does this project follow? → sense_conventions"
  }
}
```

### Two-Layer Strategy

| Layer | What | When |
|---|---|---|
| **`deep-explore` agent** | Purpose-built, full instructions, optimal workflow | Parent explicitly needs codebase exploration |
| **SubagentStart hook** | Lightweight context injection (10-15 lines) | Any other subagent spawns (Explore, general-purpose, custom) |

The agent is the deep path; the hook is the broad path. Together they cover all subagent scenarios.

### Token Savings

A typical Explore or general-purpose subagent reads 10-30 files (~2k-5k tokens each) to understand a codebase area. One `sense_graph` or `sense_search` call returns the same structural information for ~500-1k tokens.

**Estimated savings: 10-50x per subagent invocation.**

For a session that spawns 3-5 subagents, this compounds to tens of thousands of tokens saved — and faster, more accurate results since Sense returns pre-indexed structural data rather than requiring the subagent to piece together understanding from raw file reads.

### Limitations

- **Can inject context, cannot modify the subagent's prompt** — but `additionalContext` is enough. Claude sees it before acting.
- **Cannot block subagent creation** — but that's the desired behavior (no red walls).
- **Cannot add system instructions** — it's context, not system prompt. In practice, Claude treats strong `additionalContext` guidance similarly.
- **Subagents still need to call `ToolSearch`** — the context tells them to, but can't pre-load the tool schemas. This is one extra round-trip, but worth it vs. 10-30 file reads.

### Synergy with Phase 1

Phase 1 switches Agent/Explore from `deny` to `systemMessage` + `additionalContext`. Phase 3 makes that even better: instead of just nudging the parent to not spawn agents, you give it a better agent to spawn (`deep-explore`), and make any other spawned agents Sense-aware from birth. All strategies reinforce each other.

---

### What to skip (for now)

- **Configurable deny-vs-warn per project** — over-engineering at this stage. Default to `systemMessage` everywhere, revisit if users ask for stricter enforcement.
- **Blocking Bash grep entirely** — too aggressive. Some greps are for exact strings where Sense isn't the right tool. The `systemMessage` nudge handles this gracefully.

## Summary

Three key changes would most improve adoption without hurting UX:

1. **SessionStart**: Inject `summary.md` content directly into hook output (eliminates the "forgot to read it" failure mode)
2. **PreToolUse**: Replace all `deny` responses with `systemMessage` + `additionalContext` (Sense becomes a helpful tip, not a red wall)
3. **SubagentStart**: Inject Sense context into every subagent (10-50x token savings per subagent, more accurate results)

The combination makes Sense visible and helpful at every touchpoint — main session, tool calls, and subagents — without ever blocking the user's workflow.
