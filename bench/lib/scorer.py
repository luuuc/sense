#!/usr/bin/env python3
"""Scenario scorer: score a Claude transcript against a scenario's checklist.

Usage: scorer.py <result_dir> <scenario.yaml> <bench_dir>

Reads transcript.json (stream-json JSONL), the scenario YAML, parses the
transcript to extract tool calls, assistant text, and file modifications.
Matches each step's checklist items against the transcript.

Writes scored.json into the result_dir.
"""

import json
import os
import re
import subprocess
import sys


# ── Transcript parsing ──────────────────────────────────────────────


def parse_transcript(path):
    """Parse a stream-json JSONL transcript into structured data.

    Returns: {
      tool_calls: [{name, input}],
      text_blocks: [str],
      usage: {input_tokens, output_tokens, cache_read_input_tokens,
              cache_creation_input_tokens},
      cost_usd: float | None,
      duration_ms: int | None,
      session_id: str | None,
    }
    """
    tool_calls = []
    text_blocks = []
    usage = {"input_tokens": 0, "output_tokens": 0,
             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}
    cost_usd = None
    duration_ms = None
    session_id = None

    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue

            if obj.get("session_id"):
                session_id = obj["session_id"]

            event = obj.get("event", obj)
            event_type = event.get("type", "")

            if event_type == "assistant":
                msg = event.get("message", event)
                for block in msg.get("content", []):
                    if block.get("type") == "tool_use":
                        tool_calls.append({
                            "name": block.get("name", "unknown"),
                            "input": block.get("input", {}),
                        })
                    elif block.get("type") == "text":
                        text = block.get("text", "")
                        if text:
                            text_blocks.append(text)

            # Result event: cost, duration, usage at top-level of obj.
            # Accumulate across result events so a multi-turn session run (one
            # result per resumed turn) reports CUMULATIVE cost/time/usage. A
            # single-turn run has exactly one result event, so this is identical
            # to the old overwrite behavior — no regression for the single-task
            # scenarios.
            if obj.get("type") == "result":
                rc = obj.get("total_cost_usd")
                if rc is not None:
                    cost_usd = (cost_usd or 0.0) + rc
                rd = obj.get("duration_ms")
                if rd is not None:
                    duration_ms = (duration_ms or 0) + rd
                result_usage = obj.get("usage", {})
                if isinstance(result_usage, dict):
                    usage["input_tokens"] += result_usage.get("input_tokens", 0) or 0
                    usage["output_tokens"] += result_usage.get("output_tokens", 0) or 0
                    usage["cache_read_input_tokens"] += result_usage.get("cache_read_input_tokens", 0) or 0
                    usage["cache_creation_input_tokens"] += result_usage.get("cache_creation_input_tokens", 0) or 0

    return {
        "tool_calls": tool_calls,
        "text_blocks": text_blocks,
        "usage": usage,
        "cost_usd": cost_usd,
        "duration_ms": duration_ms,
        "session_id": session_id,
    }


# ── Full transcript text ─────────────────────────────────────────────


def read_transcript_texts(path):
    """Read two views of a transcript.

    answer_text: assistant text blocks only — the model's actual answer.
                 This is what fairness checks (word/contains/phrase/
                 response_richness) match against, so a query like
                 Grep(pattern="TopicCreator") cannot satisfy a `contains`
                 check for "TopicCreator" by virtue of being typed.

    audit_text:  answer_text + tool inputs + parsed tool_result content.
                 Diagnostic view, exposed in metrics for debugging but
                 never fed to keyword checks.

    Claude Code's stream-json nests tool results inside
    `user.message.content[*].type == "tool_result"`, with `content` either
    a string or a list of `{type:"text", text:"..."}` blocks. The previous
    implementation looked for a top-level `tool_result` event that never
    fires, silently hiding tool output from audit_text.
    """
    answer_parts = []
    audit_parts = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue

            event = obj.get("event", obj)
            event_type = event.get("type", "")

            if event_type == "assistant":
                for block in event.get("message", {}).get("content", []):
                    if block.get("type") == "text":
                        text = block.get("text", "")
                        if text:
                            answer_parts.append(text)
                            audit_parts.append(text)
                    elif block.get("type") == "tool_use":
                        audit_parts.append(json.dumps(block.get("input", {})))

            elif event_type == "user":
                for block in event.get("message", {}).get("content", []) or []:
                    if not isinstance(block, dict) or block.get("type") != "tool_result":
                        continue
                    content = block.get("content", "")
                    if isinstance(content, str):
                        audit_parts.append(content)
                    elif isinstance(content, list):
                        for c in content:
                            if isinstance(c, dict):
                                audit_parts.append(c.get("text", ""))
                            elif isinstance(c, str):
                                audit_parts.append(c)

    return "\n".join(answer_parts), "\n".join(audit_parts)


# ── Check evaluation ─────────────────────────────────────────────────

TOOL_USED_PATTERNS = [
    r"sense_graph", r"sense_search", r"sense_blast", r"sense_conventions",
    r"mcp__sense", r"sense_sense",
]

FALLBACK_NAMES = {"Grep", "grep", "rg", "Glob", "glob", "find", "Agent"}


def evaluate_check(check, transcript_text, tool_calls, repo_path=None):
    """Evaluate a single checklist item.

    Supported check types:
      contains         - value appears anywhere in transcript (case-insensitive,
                         no boundary — matches inside identifiers)
      phrase           - case-insensitive substring with non-word boundaries
                         on both sides — preferred over `contains` for short
                         tokens that would otherwise leak into identifiers
                         (e.g. "ensure" matching "EnsureMagic")
      word             - value appears as whole word (word boundary match)
      starts_with      - any line in transcript starts with value
      mcp_tool_used    - tool name appears in tool_calls
      no_grep          - grep was NEVER used in the session
      exact            - value appears verbatim
      transcript_contains - like contains, aliased
      diff_contains    - value appears in git diff output
    """
    ctype = check["type"]
    value = check["value"]

    if ctype == "contains" or ctype == "transcript_contains":
        return {"hit": value.lower() in transcript_text.lower(), "type": ctype, "value": value}

    elif ctype == "phrase":
        pattern = r'(?<!\w)' + re.escape(value) + r'(?!\w)'
        return {"hit": bool(re.search(pattern, transcript_text, re.IGNORECASE)), "type": ctype, "value": value}

    elif ctype == "word":
        pattern = r'(?<!\w)' + re.escape(value) + r'(?!\w)'
        return {"hit": bool(re.search(pattern, transcript_text, re.IGNORECASE)), "type": ctype, "value": value}

    elif ctype == "starts_with":
        hit = any(
            line.strip().lower().startswith(value.lower())
            for line in transcript_text.splitlines()
        )
        return {"hit": hit, "type": ctype, "value": value}

    elif ctype == "mcp_tool_used":
        hit = any(value in tc.get("name", "") for tc in tool_calls)
        return {"hit": hit, "type": ctype, "value": value}

    elif ctype == "no_grep":
        grep_used = any(
            tc["name"] in FALLBACK_NAMES
            or "grep" in (tc.get("input", {}).get("command", "") if isinstance(tc.get("input"), dict) else "")
            or "rg " in (tc.get("input", {}).get("command", "") if isinstance(tc.get("input"), dict) else "")
            for tc in tool_calls
        )
        return {"hit": not grep_used, "type": ctype, "value": value}

    elif ctype == "exact":
        return {"hit": value in transcript_text, "type": ctype, "value": value}

    elif ctype == "diff_contains" and repo_path:
        try:
            raw = subprocess.run(
                ["git", "diff", "--unified=0"],
                capture_output=True, text=True, cwd=repo_path, timeout=10,
            )
            diff_text = raw.stdout + raw.stderr
            hit = value in diff_text
            return {"hit": hit, "type": ctype, "value": value, "diff": diff_text[:2000]}
        except Exception:
            return {"hit": False, "type": ctype, "value": value, "error": "git diff failed"}

    elif ctype == "response_richness":
        return _check_richness(transcript_text, int(value))

    return {"hit": False, "type": ctype, "value": value}


# ── Response richness ────────────────────────────────────────────────

_SOURCE_FILE_RE = re.compile(
    r'([\w/\-_.]+\.(?:py|go|rs|java|kt|rb|ts|tsx|js|jsx|c|h|cpp|hpp|swift|scala|cs|vue|svelte|ex|exs))'
    r'\s*[:>]\s*(\d+|[\w.#:]+)'
)


def _check_richness(transcript_text, min_files):
    """Count unique source files referenced in structured output.

    Matches patterns like:
      app.py:1566
      context.go:Context.Next
      lib/post_creator.rb:PostCreator#create
      axum-core/src/extract/mod.rs:FromRequest
      base-server.ts:handleRequest

    Excludes .md, .txt, .json, .yaml, .yml, .toml, .lock, .html files.
    """
    matches = _SOURCE_FILE_RE.findall(transcript_text)
    excluded_exts = {'.md', '.txt', '.json', '.yaml', '.yml', '.toml',
                     '.lock', '.html', '.css', '.scss', '.less',
                     '.svg', '.png', '.jpg', '.jpeg', '.gif', '.ico'}
    unique_files = set()
    unique_refs = set()
    for filepath, ref in matches:
        ext = os.path.splitext(filepath)[1].lower()
        if ext not in excluded_exts:
            unique_files.add(filepath)
            unique_refs.add(f"{filepath}:{ref}")

    hit = len(unique_files) >= min_files
    return {
        "hit": hit,
        "type": "response_richness",
        "value": str(min_files),
        "unique_files": len(unique_files),
        "unique_refs": len(unique_refs),
        "threshold": min_files,
    }


# ── Miss detection ───────────────────────────────────────────────────


def detect_misses(tool_calls):
    """Classify non-MCP search activity by position in the session.

    This block is diagnostic only — it does not feed any score. The
    adoption layer's `tool_fluency = mcp / (mcp + grep)` is the single
    penalty for reaching for grep/Glob, and it counts every such call
    regardless of position. The breakdown below exists so a human
    reading `scored.json` can see the *shape* of the session.

    Categories:
      pre_mcp_misses              - grep/Glob/Read of source BEFORE first MCP call
      post_mcp_verification_reads - Read calls AFTER first MCP call
      post_mcp_misses             - grep/Glob calls AFTER first MCP call
    """
    first_mcp_idx = None
    for i, tc in enumerate(tool_calls):
        name = tc.get("name", "")
        if any(re.search(p, name) for p in TOOL_USED_PATTERNS):
            first_mcp_idx = i
            break

    mcp_used = first_mcp_idx is not None

    pre_misses = []
    post_reads = []
    post_other = []

    for i, tc in enumerate(tool_calls):
        name = tc.get("name", "")
        inp = tc.get("input", {})
        cmd = ""
        if isinstance(inp, dict):
            cmd = inp.get("command", "") or inp.get("cmd", "") or ""

        is_read_source = False
        if name == "Read" and isinstance(inp, dict):
            fp = inp.get("filePath", "") or inp.get("file_path", "")
            is_read_source = bool(fp) and ".summary.md" not in fp

        is_grep = name in FALLBACK_NAMES or "grep" in cmd or "rg " in cmd
        is_glob = name == "Glob"
        is_fallback = is_grep or is_glob or is_read_source

        if not is_fallback:
            continue

        desc = name
        if is_grep:
            txt = cmd[:60] if cmd else str(inp.get("pattern", ""))[:40]
            desc = f"{name}({txt})" if txt else name
        elif is_read_source:
            fp = ""
            if isinstance(inp, dict):
                fp = inp.get("filePath", "") or inp.get("file_path", "")
            base = os.path.basename(fp) if fp else "?"
            desc = f"Read({base})"

        if mcp_used and is_read_source and first_mcp_idx is not None and i > first_mcp_idx:
            post_reads.append(desc)
        elif mcp_used and is_fallback:
            if first_mcp_idx is None or i < first_mcp_idx:
                pre_misses.append(desc)
            else:
                post_other.append(desc)
        # If no MCP at all, don't count anything (can't bypass what isn't available)

    return {
        "pre_mcp_misses": pre_misses,
        "post_mcp_verification_reads": post_reads,
        "post_mcp_misses": post_other,
        "has_mcp_tools": mcp_used,
        "detail": (
            f"pre-MCP bypasses: {len(pre_misses)}, "
            f"post-MCP verification reads: {len(post_reads)}, "
            f"post-MCP misses: {len(post_other)} "
            f"(diagnostic only — grep_count drives fluency)"
        ),
    }


# ── Step evaluation ─────────────────────────────────────────────────


def evaluate_step(step, transcript_text, tool_calls, repo_path=None):
    """Evaluate all checks for one step.

    Checks tagged layer: adoption are tracked separately for the adoption score.
    """
    checks = step.get("checks", [])
    results = []
    hits_required = 0
    hits_bonus = 0
    total_required = 0
    total_bonus = 0
    fairness_hits_required = 0
    fairness_hits_bonus = 0
    fairness_total_required = 0
    fairness_total_bonus = 0

    for check in checks:
        result = evaluate_check(check, transcript_text, tool_calls, repo_path)
        result["layer"] = check.get("layer", "fairness")
        results.append(result)

        is_adoption = check.get("layer") == "adoption"
        required = check.get("required", True)

        if required:
            total_required += 1
            if result["hit"]:
                hits_required += 1
            if not is_adoption:
                fairness_total_required += 1
                if result["hit"]:
                    fairness_hits_required += 1
        else:
            total_bonus += 1
            if result["hit"]:
                hits_bonus += 1
            if not is_adoption:
                fairness_total_bonus += 1
                if result["hit"]:
                    fairness_hits_bonus += 1

    step_result = {
        "name": step.get("name", "unnamed"),
        "checks": results,
        "hits_required": hits_required,
        "total_required": total_required,
        "hits_bonus": hits_bonus,
        "total_bonus": total_bonus,
        "fairness_hits_required": fairness_hits_required,
        "fairness_total_required": fairness_total_required,
        "fairness_hits_bonus": fairness_hits_bonus,
        "fairness_total_bonus": fairness_total_bonus,
        "score_required": round((hits_required / total_required), 4) if total_required > 0 else 1.0,
        "score_bonus": round((hits_bonus / total_bonus), 4) if total_bonus > 0 else 0.0,
    }

    step_result["combined_score"] = round(
        (hits_required + 0.5 * hits_bonus) / max(1, total_required + 0.5 * total_bonus), 4
    )

    step_result["fairness_score"] = round(
        (fairness_hits_required + 0.5 * fairness_hits_bonus)
        / max(1, fairness_total_required + 0.5 * fairness_total_bonus), 4
    )

    return step_result


# ── Main scoring ────────────────────────────────────────────────────


EFFICIENCY_CEILINGS = {
    "flask": 15_000,
    "gin": 15_000,
    "javalin": 15_000,
    "axum": 20_000,
    "discourse": 30_000,
    "nextjs": 40_000,
}

DEFAULT_EFFICIENCY_CEILING = 30_000

# Wall-time ceilings, in seconds — the "code map" advantage shows up as
# faster sessions, so time is half of efficiency. Picked at ~3-4× a healthy
# Sense session for each repo, so a fast tool scores ~0.7 and a slow one
# (multi-thousand-second baseline run) collapses to 0.
# Reduced 20% from the pitch's original values after observed sessions ran
# well inside the previous ceilings (see Card 15 e2e: mean 145s on iter-1
# sessions, vs. flask's previous 400s ceiling). New numbers tighten the
# efficiency signal without truncating healthy sessions.
TIME_CEILINGS = {
    "flask": 320,        # was 400
    "gin": 320,          # was 400
    "axum": 480,         # was 600
    "javalin": 480,      # was 600
    "discourse": 480,    # was 600
    "nextjs": 720,       # was 900
}

DEFAULT_TIME_CEILING = 480  # was 600

# Per-repo --max-budget-usd, in dollars. Sized at ~2× the typical observed
# healthy session cost on each repo, so genuine cost-runaway sessions still
# get cut but the cap stops being the primary discriminator. From Card 15
# iter-1 Phase 1 observation (fresh cache): flask/gin ~$0.65, axum/javalin
# ~$1.00, discourse ~$1.10, nextjs ~$1.20 typical. 2× buffer keeps
# healthy sessions intact while bounding worst-case session spend at
# ~$2.25 (i.e. a runaway nextjs caps at ~2× its mean).
BUDGET_PER_REPO = {
    "flask":     1.00,
    "gin":       1.00,
    "axum":      1.75,
    "javalin":   1.75,
    "discourse": 2.00,
    "nextjs":    2.25,
}

DEFAULT_BUDGET_USD = 1.50  # unknown repos (e.g. held-out sense) — sits at the median.

# Claude pricing per million tokens. Used to estimate cost on failed runs
# whose transcript never emitted a final total_cost_usd. Update when
# pricing or the default model changes — these are Opus 4.x rates.
PRICE_PER_M = {
    "input": 15.00,
    "output": 75.00,
    "cache_read": 1.50,
    "cache_write": 18.75,
}


def sum_partial_usage(transcript_path):
    """Sum token usage across all assistant messages.

    A successful session reports cumulative usage in the final `result`
    event, but a session that timed out or crashed never emits that event.
    For failed runs we have to add up the per-message usage instead so the
    cost of partial work is still reflected.
    """
    usage = {"input_tokens": 0, "output_tokens": 0,
             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}
    with open(transcript_path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue
            event = obj.get("event", obj)
            msg = event.get("message") or {}
            mu = msg.get("usage") or {}
            for k in usage:
                usage[k] += mu.get(k, 0) or 0
    return usage


def estimate_cost(usage):
    return (
        usage.get("input_tokens", 0) * PRICE_PER_M["input"]
        + usage.get("output_tokens", 0) * PRICE_PER_M["output"]
        + usage.get("cache_read_input_tokens", 0) * PRICE_PER_M["cache_read"]
        + usage.get("cache_creation_input_tokens", 0) * PRICE_PER_M["cache_write"]
    ) / 1_000_000


def _repo_file_list(checkout):
    """Repo-relative file paths under a checkout, for the gold path-compaction
    oracle (gold.py). Prunes VCS/index/vendor dirs. Returns [] if the checkout
    is missing so the caller leaves the oracle off. A broader file set than the
    code-only .sense index can only make a suffix MORE ambiguous (turning a
    credit off), never manufacture one, so walking the tree stays sound.
    """
    if not checkout or not os.path.isdir(checkout):
        return []
    out = []
    for root, dirs, files in os.walk(checkout):
        dirs[:] = [d for d in dirs if d not in (".git", ".sense", "node_modules", ".bundle")]
        for fn in files:
            out.append(os.path.relpath(os.path.join(root, fn), checkout))
    return out


def score_transcript(transcript_path, scenario, repo_path=None, repo_checkout=None,
                     overhead=None):
    """Score a transcript against a scenario.

    Two-layer scoring:
      fairness components — keyword_coverage, citation_grounding,
                            efficiency. llm_quality is filled in by
                            judge.py; the combined fairness_score is
                            computed by the reporter via fairness.compute.
                            Skips checks tagged layer: adoption.
      adoption_score      — tool_fluency (0.60) + discoverability (0.40)
                            For code-intel-vs-code-intel comparisons only.

    repo_path     — the result_dir, used as cwd for `diff_contains` checks.
    repo_checkout — the cloned repo at run_meta.repo_commit, used by
                    grounding to verify citations. Optional: if missing,
                    citation_grounding reports total but skips verification.
    overhead      — optional dict from a build-time setup step that burned
                    LLM tokens/time/cost before the scoring session
                    started (currently: serena's onboarding pre-run).
                    When present, its `token_total_billed`, `wall_time_seconds`,
                    and `cost_usd` are added to the scoring session's totals
                    BEFORE efficiency is computed, so a tool whose
                    recommended install requires LLM onboarding pays for
                    it in the efficiency axis like any other token spend.
                    Expected shape — see docker/lib/parse-claude-result.py.
    """
    from grounding import ground_citations

    t = parse_transcript(transcript_path)
    answer_text, audit_text = read_transcript_texts(transcript_path)

    tool_calls = t["tool_calls"]
    usage = t["usage"]

    billed_tokens = usage["input_tokens"] + usage["output_tokens"]

    # Optional pre-session overhead (e.g. serena's onboarding pre-run that
    # writes .serena/memories/ at image build time). Add it to the billed
    # tokens + wall_time used to compute efficiency so the comparison
    # against tools without LLM-driven setup stays fair. The raw scoring-
    # session numbers are preserved in the metrics block alongside the
    # overhead breakdown, so downstream readers can decompose either way.
    overhead = overhead or {}
    overhead_billed = int(overhead.get("token_total_billed") or 0)
    overhead_wall   = float(overhead.get("wall_time_seconds") or 0.0)
    overhead_cost   = float(overhead.get("cost_usd") or 0.0)

    step_results = []
    for step in scenario.get("steps", []):
        sr = evaluate_step(step, answer_text, tool_calls, repo_path)
        step_results.append(sr)

    misses = detect_misses(tool_calls)

    completeness = sum(s["combined_score"] for s in step_results) / max(len(step_results), 1)

    # True hit rate across all fairness checks: a 10-check step now carries
    # ten times the weight of a 1-check step. Bonus checks weighted at 0.5
    # to match the per-step combined_score formula. The previous step-mean
    # is preserved as `step_avg_score` for anyone who wants it.
    fair_hits = sum(s["fairness_hits_required"] + 0.5 * s["fairness_hits_bonus"] for s in step_results)
    fair_total = sum(s["fairness_total_required"] + 0.5 * s["fairness_total_bonus"] for s in step_results)
    keyword_coverage = (fair_hits / fair_total) if fair_total > 0 else 1.0
    step_avg_score = sum(s["fairness_score"] for s in step_results) / max(len(step_results), 1)

    repo = scenario.get("repo", "")
    ceiling = EFFICIENCY_CEILINGS.get(repo, DEFAULT_EFFICIENCY_CEILING)
    time_ceiling = TIME_CEILINGS.get(repo, DEFAULT_TIME_CEILING)

    wall_time = round((t.get("duration_ms", 0) or 0) / 1000, 1)

    # Effective totals for efficiency: scoring session + any overhead the
    # tool's recommended setup burns (serena onboarding). For tools with no
    # overhead, these equal the session-only numbers.
    eff_billed = billed_tokens + overhead_billed
    eff_wall   = wall_time + overhead_wall

    # Zero tokens or zero wall-time means no measurable work — treat each as
    # a zero in its half of efficiency, not as perfect efficiency.
    token_eff = max(0.0, 1.0 - (eff_billed / ceiling)) if eff_billed > 0 else 0.0
    time_eff = max(0.0, 1.0 - (eff_wall / time_ceiling)) if eff_wall > 0 else 0.0
    efficiency = 0.5 * token_eff + 0.5 * time_eff

    grep_count = 0
    read_count = 0
    mcp_count = 0
    for tc in tool_calls:
        name = tc.get("name", "")
        inp = tc.get("input", {})
        cmd = ""
        if isinstance(inp, dict):
            cmd = inp.get("command", "") or ""
        if name.startswith("mcp__"):
            mcp_count += 1
        elif name in ("Grep", "Glob") or "grep" in cmd or "rg " in cmd:
            # Glob and Grep both count as conventional search for fluency —
            # a tool that satisfies a "find this code" task with Glob is
            # still bypassing the code-intel layer.
            grep_count += 1
        elif name == "Read":
            fp = ""
            if isinstance(inp, dict):
                fp = inp.get("filePath", "") or inp.get("file_path", "")
            if ".summary.md" not in fp:
                read_count += 1

    if mcp_count + grep_count > 0:
        tool_fluency = mcp_count / (mcp_count + grep_count)
    else:
        tool_fluency = 0.5

    # Saturate at 20 unique files rather than 10. The previous ceiling gave
    # every reasonably-rich answer a free 1.0 — Sense surfaced 18 files in
    # discourse and was indistinguishable from a hypothetical 11. The richer
    # "novel files surfaced by MCP tool_result" notion is deferred to 20-06
    # once tool_result parsing has a few iterations of wear.
    total_richness = _check_richness(answer_text, 0).get("unique_files", 0)
    discoverability = min(1.0, total_richness / 20.0)

    adoption_score = 0.60 * tool_fluency + 0.40 * discoverability

    # Citation grounding. Folded into combined fairness via fairness.py
    # (post-20-05). The combined score is computed by the reporter from
    # scored.json + judged.json; scorer.py emits the components only.
    citation_grounding = ground_citations(answer_text, repo_checkout)

    # Gold-target recall (reported alongside fairness, never folded into it).
    # Measures coverage against a fixed per-scenario denominator, unlike
    # citation_grounding's grounded/emitted rate. None when no gold declared.
    from gold import score_gold_recall
    # Path-compaction oracle inputs: the real repo file tree (from repo_checkout)
    # and the raw transcript. Both gate a credit, so an agent that shortened a
    # path it genuinely held (full path in the transcript) is re-credited, while
    # a fabricated path is not. When repo_checkout is absent the oracle is off
    # and gold_recall is byte-identical to the literal matcher. See gold.py.
    gold_repo_files = _repo_file_list(repo_checkout) if repo_checkout else None
    gold_transcript = None
    if gold_repo_files:
        try:
            with open(transcript_path, encoding="utf-8", errors="ignore") as fh:
                gold_transcript = fh.read()
        except OSError:
            gold_repo_files = None
    gold_recall = score_gold_recall(answer_text, scenario.get("gold"),
                                    repo_files=gold_repo_files,
                                    transcript_text=gold_transcript)
    # NOTE: gold_f1 was DROPPED (2026-06-19). Its precision punished Sense for
    # citing real dependents that the curated discriminator gold deliberately
    # omits (gitlabhq P=0.14 at R=0.91), so it under-credited Sense's core
    # completeness win. cited_recall (gold-bounded) is the headline; see
    # bench/SCORING.md. gold.score_gold_f1 is retained (tested) but unused.

    scored = {
        "scenario": scenario["name"],
        "repo": scenario["repo"],
        "adoption_score": round(adoption_score, 4),
        "keyword_coverage": round(keyword_coverage, 4),
        "step_avg_score": round(step_avg_score, 4),
        "efficiency": round(efficiency, 4),
        "completeness": round(completeness, 4),
        "tool_fluency": round(tool_fluency, 4),
        "discoverability": round(discoverability, 4),
        "citation_grounding": citation_grounding,
        "gold_recall": gold_recall,
        "steps": step_results,
        "misses": misses,
        "metrics": {
            "tool_calls": len(tool_calls),
            "grep_count": grep_count,
            "read_count": read_count,
            "mcp_count": mcp_count,
            "token_input_uncached": usage["input_tokens"],
            "token_output": usage["output_tokens"],
            "token_cache_read": usage["cache_read_input_tokens"],
            "token_cache_write": usage["cache_creation_input_tokens"],
            "token_total_billed": billed_tokens,
            "token_total_all": billed_tokens + usage["cache_read_input_tokens"] + usage["cache_creation_input_tokens"],
            "wall_time_seconds": wall_time,
            "cost_usd": round(
                (t["cost_usd"] if t.get("cost_usd") is not None else 0.0)
                + overhead_cost, 4
            ) if (t.get("cost_usd") is not None or overhead_cost > 0) else None,
            # Effective totals (session + overhead) — what efficiency is
            # actually computed against. Equal to the session-only numbers
            # for any tool without an overhead block.
            "effective_token_total_billed": eff_billed,
            "effective_wall_time_seconds": round(eff_wall, 1),
            "efficiency_ceiling": ceiling,
            "time_ceiling_seconds": time_ceiling,
            "token_efficiency": round(token_eff, 4),
            "time_efficiency": round(time_eff, 4),
            "answer_chars": len(answer_text),
            "audit_chars": len(audit_text),
            # Per-tool setup overhead folded into efficiency above. Absent
            # for tools whose recommended install spends no LLM tokens.
            "overhead": ({
                "source": overhead.get("source"),
                "token_total_billed": overhead_billed,
                "wall_time_seconds": overhead_wall,
                "cost_usd": round(overhead_cost, 4),
                "num_turns": overhead.get("num_turns"),
            } if overhead else None),
        },
    }

    return scored


if __name__ == "__main__":
    if len(sys.argv) < 4:
        print(
            "Usage: scorer.py <result_dir> <scenario.yaml> <bench_dir> "
            "[repo_checkout]",
            file=sys.stderr,
        )
        sys.exit(1)

    result_dir = sys.argv[1]
    scenario_path = sys.argv[2]
    bench_dir = sys.argv[3]
    repo_checkout = sys.argv[4] if len(sys.argv) > 4 else None

    sys.path.insert(0, os.path.join(bench_dir, "lib"))
    from scenario import parse as parse_scenario

    scenario = parse_scenario(scenario_path)

    transcript_path = os.path.join(result_dir, "transcript.json")
    if not os.path.exists(transcript_path):
        print(json.dumps({"error": "transcript.json not found"}))
        sys.exit(1)

    # A failed Claude session leaves a partial transcript that would
    # otherwise be scored as a low-effort answer. Treat it as a hard zero
    # so the failure surfaces in the report instead of getting credit for
    # any keywords that happened to leak into the partial output.
    meta_path = os.path.join(result_dir, "run_meta.json")
    run_meta = {}
    if os.path.exists(meta_path):
        try:
            with open(meta_path) as f:
                run_meta = json.load(f)
        except (json.JSONDecodeError, OSError):
            run_meta = {}

    # Optional pre-session overhead JSON written by each tool's entrypoint
    # when the tool's recommended install includes an LLM step (serena's
    # onboarding pre-run is the only current consumer). Absent file =
    # zero overhead, scored exactly as before.
    overhead_path = os.path.join(result_dir, "onboarding-overhead.json")
    overhead_data = None
    if os.path.exists(overhead_path):
        try:
            with open(overhead_path) as f:
                overhead_data = json.load(f)
        except (json.JSONDecodeError, OSError):
            overhead_data = None

    if run_meta.get("error"):
        # The session was interrupted (over-budget, wall-time timeout, or a
        # real crash). Inspect the transcript to distinguish:
        #   - has assistant text + final `result` event: claude exited
        #     non-zero AFTER the model emitted a complete answer (typical
        #     over-budget signature). Score it.
        #   - has assistant text, no `result` event: truncated mid-stream.
        #     Score what was emitted; patch in partial-usage metrics so
        #     cost/efficiency reflect real spend.
        #   - no assistant text: real crash. Keep the legacy zero behavior.
        # Rationale: an exam done under constraint still gets graded on
        # what was written; it doesn't get a zero for hitting the time
        # limit. `constrained: True` carries that signal in reports and
        # audits without poisoning the fairness score with false zeros.
        partial = parse_transcript(transcript_path)
        has_answer = bool(partial["text_blocks"])
        has_result_event = partial["cost_usd"] is not None

        if not has_answer:
            # True crash — no model output. Zero everything (legacy path).
            partial_usage = sum_partial_usage(transcript_path)
            partial_cost = estimate_cost(partial_usage)
            billed_tokens = partial_usage["input_tokens"] + partial_usage["output_tokens"]
            all_tokens = (billed_tokens
                          + partial_usage["cache_read_input_tokens"]
                          + partial_usage["cache_creation_input_tokens"])
            scored = {
                "scenario": scenario["name"],
                "repo": scenario["repo"],
                "failed": True,
                "failure_reason": run_meta.get("error"),
                "adoption_score": 0.0,
                "keyword_coverage": 0.0,
                "step_avg_score": 0.0,
                "efficiency": 0.0,
                "completeness": 0.0,
                "tool_fluency": 0.0,
                "discoverability": 0.0,
                "steps": [],
                "misses": {},
                "metrics": {
                    "tool_calls": len(partial["tool_calls"]),
                    "grep_count": 0,
                    "read_count": 0,
                    "mcp_count": 0,
                    "token_input_uncached": partial_usage["input_tokens"],
                    "token_output": partial_usage["output_tokens"],
                    "token_cache_read": partial_usage["cache_read_input_tokens"],
                    "token_cache_write": partial_usage["cache_creation_input_tokens"],
                    "token_total_billed": billed_tokens,
                    "token_total_all": all_tokens,
                    "wall_time_seconds": float(run_meta.get("wall_time_seconds") or 0),
                    "cost_usd": round(partial_cost, 4),
                    "cost_estimated": partial["cost_usd"] is None,
                    "efficiency_ceiling": EFFICIENCY_CEILINGS.get(
                        scenario.get("repo", ""), DEFAULT_EFFICIENCY_CEILING),
                },
            }
        else:
            # Score the constrained session normally.
            scored = score_transcript(
                transcript_path, scenario, result_dir,
                repo_checkout=repo_checkout, overhead=overhead_data,
            )
            scored["failed"] = False
            scored["constrained"] = True
            scored["constraint_reason"] = run_meta.get("error")
            if not has_result_event:
                # Truncated mid-stream: the result event never fired, so
                # parse_transcript saw zero usage/cost. Recover real spend
                # from per-message usage and patch the metrics so cost
                # tracking and the efficiency axis don't under-report.
                partial_usage = sum_partial_usage(transcript_path)
                billed = partial_usage["input_tokens"] + partial_usage["output_tokens"]
                m = scored["metrics"]
                m["token_input_uncached"] = partial_usage["input_tokens"]
                m["token_output"] = partial_usage["output_tokens"]
                m["token_cache_read"] = partial_usage["cache_read_input_tokens"]
                m["token_cache_write"] = partial_usage["cache_creation_input_tokens"]
                m["token_total_billed"] = billed
                m["token_total_all"] = (
                    billed
                    + partial_usage["cache_read_input_tokens"]
                    + partial_usage["cache_creation_input_tokens"]
                )
                m["cost_usd"] = round(estimate_cost(partial_usage), 4)
                m["cost_estimated"] = True
                m["wall_time_seconds"] = float(run_meta.get("wall_time_seconds") or 0)
    else:
        scored = score_transcript(
            transcript_path, scenario, result_dir,
            repo_checkout=repo_checkout, overhead=overhead_data,
        )

    output_path = os.path.join(result_dir, "scored.json")
    with open(output_path, "w") as f:
        json.dump(scored, f, indent=2)
        f.write("\n")

    print(f"Scored: {scenario['name']} → {output_path}", file=sys.stderr)
    print(json.dumps(scored, indent=2))
