#!/usr/bin/env python3
"""Scenario scorer: score a Claude transcript against a scenario's checklist.

Usage: scorer.py <result_dir> <scenario.yaml> <bench2_dir>

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

            # Result event: cost, duration, usage at top-level of obj
            if obj.get("type") == "result":
                cost_usd = obj.get("total_cost_usd", cost_usd)
                duration_ms = obj.get("duration_ms", duration_ms)
                result_usage = obj.get("usage", {})
                if isinstance(result_usage, dict):
                    if result_usage.get("input_tokens"):
                        usage["input_tokens"] = result_usage["input_tokens"]
                    if result_usage.get("output_tokens"):
                        usage["output_tokens"] = result_usage["output_tokens"]
                    if result_usage.get("cache_read_input_tokens"):
                        usage["cache_read_input_tokens"] = result_usage["cache_read_input_tokens"]
                    if result_usage.get("cache_creation_input_tokens"):
                        usage["cache_creation_input_tokens"] = result_usage["cache_creation_input_tokens"]

    return {
        "tool_calls": tool_calls,
        "text_blocks": text_blocks,
        "usage": usage,
        "cost_usd": cost_usd,
        "duration_ms": duration_ms,
        "session_id": session_id,
    }


# ── Full transcript text ─────────────────────────────────────────────


def read_full_transcript(path):
    """Read all text content from a transcript (tool outputs + assistant text)."""
    all_text = []
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
                        all_text.append(block.get("text", ""))
                    elif block.get("type") == "tool_use":
                        all_text.append(json.dumps(block.get("input", {})))

            elif event_type == "tool_result":
                content = event.get("message", {}).get("content", "")
                if isinstance(content, str):
                    all_text.append(content)
                elif isinstance(content, list):
                    for c in content:
                        if isinstance(c, dict):
                            all_text.append(c.get("text", ""))
                        elif isinstance(c, str):
                            all_text.append(c)

    return "\n".join(all_text)


# ── Check evaluation ─────────────────────────────────────────────────

TOOL_USED_PATTERNS = [
    r"sense_graph", r"sense_search", r"sense_blast", r"sense_conventions",
    r"mcp__sense", r"sense_sense",
]

FALLBACK_NAMES = {"Grep", "grep", "rg", "Glob", "glob", "find", "Agent"}


def evaluate_check(check, transcript_text, tool_calls, repo_path=None):
    """Evaluate a single checklist item.

    Supported check types:
      contains         - value appears anywhere in transcript (case-insensitive)
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
    """Distinguish real MCP bypasses from post-MCP verification reads.

    Real miss = grep/Glob/find/Read BEFORE any MCP tool was used,
    or grep/Glob/find at any point. Reading source files AFTER using
    MCP is verification, not a bypass.

    Returns:
      total              - real misses (pre-MCP bypasses + post-MCP greps)
      pre_mcp_misses     - bypasses made before first MCP call
      post_mcp_verification_reads - Read calls after MCP (not penalised)
      post_mcp_misses    - grep/Glob calls after MCP (still penalised)
      detail             - human-readable summary
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

    total_misses = len(pre_misses) + len(post_other)

    return {
        "total": total_misses,
        "pre_mcp_misses": pre_misses,
        "post_mcp_verification_reads": post_reads,
        "post_mcp_misses": post_other,
        "has_mcp_tools": mcp_used,
        "detail": (
            f"pre-MCP bypasses: {len(pre_misses)}, "
            f"post-MCP verification reads: {len(post_reads)} (not penalised), "
            f"post-MCP misses: {len(post_other)}"
        ),
    }


# ── Step evaluation ─────────────────────────────────────────────────


def evaluate_step(step, transcript_text, tool_calls, repo_path=None):
    """Evaluate all checks for one step."""
    checks = step.get("checks", [])
    results = []
    hits_required = 0
    hits_bonus = 0
    total_required = 0
    total_bonus = 0

    for check in checks:
        result = evaluate_check(check, transcript_text, tool_calls, repo_path)
        results.append(result)

        if check.get("required", True):
            total_required += 1
            if result["hit"]:
                hits_required += 1
        else:
            total_bonus += 1
            if result["hit"]:
                hits_bonus += 1

    step_result = {
        "name": step.get("name", "unnamed"),
        "checks": results,
        "hits_required": hits_required,
        "total_required": total_required,
        "hits_bonus": hits_bonus,
        "total_bonus": total_bonus,
        "score_required": round((hits_required / total_required), 4) if total_required > 0 else 1.0,
        "score_bonus": round((hits_bonus / total_bonus), 4) if total_bonus > 0 else 0.0,
    }

    step_result["combined_score"] = round(
        (hits_required + 0.5 * hits_bonus) / max(1, total_required + 0.5 * total_bonus), 4
    )

    # Extract richness from any response_richness check results in this step
    richness = max(
        (c.get("unique_files", 0) for c in step_result["checks"] if c.get("type") == "response_richness"),
        default=0,
    )
    step_result["richness"] = richness

    return step_result


# ── Main scoring ────────────────────────────────────────────────────


def score_transcript(transcript_path, scenario, repo_path=None):
    """Score a transcript against a scenario.

    Score = completeness (checklist hit rate) × efficiency (tokens, time).
    Misses, grep counts, and MCP usage are reported as supplementary metrics
    but do NOT penalise the score. Code intelligence tools are enablers,
    not grep replacements.
    """
    t = parse_transcript(transcript_path)
    full_text = read_full_transcript(transcript_path)

    tool_calls = t["tool_calls"]
    usage = t["usage"]

    billed_tokens = usage["input_tokens"] + usage["output_tokens"]

    step_results = []
    for step in scenario.get("steps", []):
        sr = evaluate_step(step, full_text, tool_calls, repo_path)
        sr["tool_calls_count"] = len(tool_calls)
        sr["token_total"] = billed_tokens + usage["cache_read_input_tokens"] + usage["cache_creation_input_tokens"]
        step_results.append(sr)

    misses = detect_misses(tool_calls)

    completeness = sum(s["combined_score"] for s in step_results) / max(len(step_results), 1)

    efficiency = 1.0
    if billed_tokens > 8000:
        efficiency = max(0.0, 1.0 - (billed_tokens / 60000))

    wall_time = round((t.get("duration_ms", 0) or 0) / 1000, 1)

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
        elif name == "Grep" or "grep" in cmd or "rg " in cmd:
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

    total_richness = max((s.get("richness", 0) for s in step_results), default=0)
    discoverability = min(1.0, total_richness / 10.0)

    weights = scenario.get("scoring", {}).get("weights", {})
    w_comp = weights.get("completeness", 0.40)
    w_eff  = weights.get("efficiency", 0.25)
    w_flu  = weights.get("tool_fluency", 0.20)
    w_disc = weights.get("discoverability", 0.15)

    overall = (w_comp * completeness + w_eff * efficiency
               + w_flu * tool_fluency + w_disc * discoverability)

    scored = {
        "scenario": scenario["name"],
        "repo": scenario["repo"],
        "overall_score": round(overall, 4),
        "completeness": round(completeness, 4),
        "efficiency": round(efficiency, 4),
        "tool_fluency": round(tool_fluency, 4),
        "discoverability": round(discoverability, 4),
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
            "cost_usd": t.get("cost_usd"),
        },
    }

    return scored


if __name__ == "__main__":
    if len(sys.argv) < 4:
        print("Usage: scorer.py <result_dir> <scenario.yaml> <bench2_dir>", file=sys.stderr)
        sys.exit(1)

    result_dir = sys.argv[1]
    scenario_path = sys.argv[2]
    bench2_dir = sys.argv[3]

    sys.path.insert(0, os.path.join(bench2_dir, "lib"))
    from scenario import parse as parse_scenario

    scenario = parse_scenario(scenario_path)

    transcript_path = os.path.join(result_dir, "transcript.json")
    if not os.path.exists(transcript_path):
        print(json.dumps({"error": "transcript.json not found"}))
        sys.exit(1)

    scored = score_transcript(transcript_path, scenario, result_dir)

    output_path = os.path.join(result_dir, "scored.json")
    with open(output_path, "w") as f:
        json.dump(scored, f, indent=2)
        f.write("\n")

    print(f"Scored: {scenario['name']} → {output_path}", file=sys.stderr)
    print(json.dumps(scored, indent=2))
