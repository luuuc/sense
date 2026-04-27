#!/usr/bin/env python3
"""Score a Claude transcript against ground truth.

Usage: scorer.py <result_dir> <bench_dir>

Reads transcript.json (stream-json JSONL), task config, and ground truth.
Writes scored.json into the same result_dir.
"""

import json
import os
import re
import sys


def parse_transcript(path):
    """Parse a stream-json JSONL transcript.

    Handles two formats:
    - Message-level: events with type "assistant"/"user"/"system"/"result"
      (Claude CLI stream-json default)
    - Streaming: events with type "content_block_start"/"delta"/"stop"
      (raw API streaming)
    """
    tool_calls = []
    text_chunks = []
    usage = {"input_tokens": 0, "output_tokens": 0,
             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}
    cost_usd = None
    duration_ms = None
    num_turns = 0
    session_id = None

    current_tool_name = None
    current_tool_input_parts = []
    in_text_block = False
    current_text_parts = []

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

            # --- Message-level format (Claude CLI) ---
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
                            text_chunks.append(text)

            # --- Streaming format (raw API) ---
            elif event_type == "message_start":
                msg = event.get("message", {})
                msg_usage = msg.get("usage", {})
                usage["input_tokens"] += msg_usage.get("input_tokens", 0)

            elif event_type == "content_block_start":
                block = event.get("content_block", {})
                if block.get("type") == "tool_use":
                    current_tool_name = block.get("name", "unknown")
                    current_tool_input_parts = []
                elif block.get("type") == "text":
                    in_text_block = True
                    current_text_parts = []

            elif event_type == "content_block_delta":
                delta = event.get("delta", {})
                if delta.get("type") == "input_json_delta":
                    current_tool_input_parts.append(delta.get("partial_json", ""))
                elif delta.get("type") == "text_delta":
                    current_text_parts.append(delta.get("text", ""))

            elif event_type == "content_block_stop":
                if current_tool_name:
                    raw_input = "".join(current_tool_input_parts)
                    try:
                        tool_input = json.loads(raw_input) if raw_input else {}
                    except json.JSONDecodeError:
                        tool_input = {"_raw": raw_input}
                    tool_calls.append({
                        "name": current_tool_name,
                        "input": tool_input,
                    })
                    current_tool_name = None
                    current_tool_input_parts = []
                if in_text_block:
                    text_chunks.append("".join(current_text_parts))
                    in_text_block = False
                    current_text_parts = []

            elif event_type == "message_delta":
                delta_usage = event.get("usage", {})
                usage["output_tokens"] += delta_usage.get("output_tokens", 0)

            # --- Result event (both formats) ---
            if obj.get("type") == "result":
                cost_usd = obj.get("total_cost_usd", cost_usd)
                duration_ms = obj.get("duration_ms", duration_ms)
                num_turns = obj.get("num_turns", num_turns)
                if obj.get("result") and not text_chunks:
                    text_chunks.append(obj["result"])
                result_usage = obj.get("usage", {})
                if result_usage.get("input_tokens"):
                    usage["input_tokens"] = result_usage["input_tokens"]
                if result_usage.get("output_tokens"):
                    usage["output_tokens"] = result_usage["output_tokens"]
                if result_usage.get("cache_read_input_tokens"):
                    usage["cache_read_input_tokens"] = result_usage[
                        "cache_read_input_tokens"
                    ]
                if result_usage.get("cache_creation_input_tokens"):
                    usage["cache_creation_input_tokens"] = result_usage[
                        "cache_creation_input_tokens"
                    ]

    final_text = text_chunks[-1] if text_chunks else ""

    return {
        "tool_calls": tool_calls,
        "final_text": final_text,
        "usage": usage,
        "cost_usd": cost_usd,
        "duration_ms": duration_ms,
        "num_turns": num_turns,
        "session_id": session_id,
    }


def classify_tool_calls(tool_calls):
    """Classify tool calls into MCP vs built-in."""
    mcp_tools = {}
    builtin_tools = {}

    for tc in tool_calls:
        name = tc["name"]
        if name.startswith("mcp__"):
            mcp_tools[name] = mcp_tools.get(name, 0) + 1
        else:
            builtin_tools[name] = builtin_tools.get(name, 0) + 1

    return {
        "total": len(tool_calls),
        "mcp_calls": sum(mcp_tools.values()),
        "builtin_calls": sum(builtin_tools.values()),
        "mcp_tools": mcp_tools,
        "builtin_tools": builtin_tools,
    }


def extract_json_from_text(text):
    """Try to extract a JSON object from Claude's response text."""
    text = text.strip()

    try:
        return json.loads(text)
    except (json.JSONDecodeError, ValueError):
        pass

    m = re.search(r"```(?:json)?\s*\n(.*?)\n```", text, re.DOTALL)
    if m:
        try:
            return json.loads(m.group(1).strip())
        except (json.JSONDecodeError, ValueError):
            pass

    return None


def _strip_go_package(file_path, symbol):
    """Strip Go package prefix from a symbol.

    Uses the directory name to identify the package. For root-level
    files (no directory), strips any leading lowercase identifier
    prefix (handles single-package repos like gin).
    """
    if "/" in file_path:
        dir_name = file_path.rsplit("/", 1)[0].rsplit("/", 1)[-1]
    else:
        dir_name = ""

    if dir_name:
        prefix = dir_name + "."
        if symbol.startswith(prefix):
            return symbol[len(prefix):]
        if file_path.endswith("_test.go"):
            test_prefix = dir_name + "_test."
            if symbol.startswith(test_prefix):
                return symbol[len(test_prefix):]
    else:
        m = re.match(r"^([a-z][a-z0-9_]*)\.(.*)", symbol)
        if m:
            return m.group(2)

    return symbol


def _strip_bare_go_package(symbol):
    """Strip Go package prefix from a bare symbol (no file path).

    Only strips 'pkg.' when followed by an uppercase letter, which
    distinguishes package.Type from type.method in Go naming.
    E.g. 'gin.Engine' -> 'Engine' but 'node.getValue' stays.
    """
    m = re.match(r"^([a-z][a-z0-9_]*)\.([A-Z].*)", symbol)
    if m:
        return m.group(2)
    return symbol


def normalize_caller(entry):
    """Normalize a caller/affected entry for comparison.

    Ground truth uses 'file:symbol' format. Claude may respond with
    'file:line symbol' or 'file:line:symbol' or other variations.
    Normalize to 'file:symbol' for comparison, stripping Go package
    prefixes so that 'pkg.Func' matches bare 'Func'.

    Also handles bare symbols (no file path) for semantic-search and
    dead-code tasks by stripping 'pkg.' when followed by uppercase.
    """
    entry = entry.strip()

    parts = entry.split()
    if len(parts) >= 2:
        file_part = parts[0].split(":")[0]
        symbol_part = parts[-1]
    elif ":" in entry:
        segments = entry.split(":")
        file_part = segments[0]
        symbol_part = segments[-1]
        if len(segments) >= 2 and segments[1].isdigit():
            symbol_part = segments[-1] if len(segments) > 2 else ""
    else:
        return _strip_bare_go_package(entry)

    symbol_part = symbol_part.rstrip(")]}")

    if not symbol_part:
        return entry

    if file_part.endswith(".go"):
        symbol_part = _strip_go_package(file_part, symbol_part)

    return file_part + ":" + symbol_part


def score_set_match(response_json, ground_truth, match_key):
    """Compare response set against ground-truth set, compute F1."""
    found_raw = response_json.get(match_key, []) if response_json else []
    expected_raw = ground_truth.get(match_key, [])

    if not isinstance(found_raw, list):
        found_raw = []
    if not isinstance(expected_raw, list):
        expected_raw = []

    found = {normalize_caller(e) for e in found_raw}
    expected = {normalize_caller(e) for e in expected_raw}

    tp = found & expected
    fp = found - expected
    fn = expected - found

    precision = len(tp) / len(found) if found else 0.0
    recall = len(tp) / len(expected) if expected else 0.0
    f1 = (2 * precision * recall / (precision + recall)) if (precision + recall) > 0 else 0.0

    return {
        "type": "set_match",
        "precision": round(precision, 4),
        "recall": round(recall, 4),
        "f1": round(f1, 4),
        "found_count": len(found),
        "expected_count": len(expected),
        "true_positives": sorted(tp),
        "false_positives": sorted(fp),
        "false_negatives": sorted(fn),
    }


TOOL_CAPABILITIES = {
    "sense": {"search", "graph", "blast", "conventions"},
    "grepai": {"search", "graph"},
    "crg": {"graph", "search"},
    "tokensave": {"search", "graph", "blast"},
    "roam": {"graph", "search"},
    "baseline": set(),
}

SEARCH_COMMANDS = re.compile(r"\b(grep|ag|rg|ack)\b")


def _recent_mcp_call(tool_calls, index, capability, window=3):
    """Check whether an MCP call for the given capability occurred
    within the preceding `window` calls — indicating a fallback/
    verification rather than a true miss."""
    start = max(0, index - window)
    for tc in tool_calls[start:index]:
        name = tc["name"]
        if not name.startswith("mcp__"):
            continue
        lower = name.lower()
        if capability == "search" and "search" in lower:
            return True
        if capability == "graph" and "graph" in lower:
            return True
        if capability == "blast" and "blast" in lower:
            return True
        if capability == "conventions" and "convention" in lower:
            return True
    return False


def detect_misses(tool_calls, tool_name):
    """Detect when Claude bypassed available MCP tools.

    A "miss" is when an MCP capability was registered but Claude used
    a slower built-in path instead: grep when search was available,
    multi-file Read when graph was available, or Agent when any
    capability was available.

    Sequence-aware: if an MCP tool was called within the preceding 3
    tool calls, the built-in call is classified as a verification/
    fallback rather than a miss.
    """
    if tool_name not in TOOL_CAPABILITIES:
        return {"total": 0, "misses": [], "by_type": {}, "unconfigured": True}
    capabilities = TOOL_CAPABILITIES[tool_name]
    if not capabilities:
        return {"total": 0, "misses": [], "by_type": {}}

    misses = []
    verifications = []
    read_count = 0
    mcp_called = any(tc["name"].startswith("mcp__") for tc in tool_calls)

    for i, tc in enumerate(tool_calls):
        name = tc["name"]
        tool_input = tc.get("input", {})

        if "search" in capabilities and name == "Bash":
            cmd = tool_input.get("command", "")
            if SEARCH_COMMANDS.search(cmd):
                entry = {
                    "type": "search_miss",
                    "tool_used": "Bash",
                    "detail": cmd[:120],
                }
                if _recent_mcp_call(tool_calls, i, "search"):
                    entry["classification"] = "verification"
                    verifications.append(entry)
                else:
                    entry["classification"] = "miss"
                    misses.append(entry)

        if "search" in capabilities and name == "Grep":
            entry = {
                "type": "search_miss",
                "tool_used": "Grep",
                "detail": tool_input.get("pattern", "")[:120],
            }
            if _recent_mcp_call(tool_calls, i, "search"):
                entry["classification"] = "verification"
                verifications.append(entry)
            else:
                entry["classification"] = "miss"
                misses.append(entry)

        if "search" in capabilities and name == "Glob":
            entry = {
                "type": "search_miss",
                "tool_used": "Glob",
                "detail": tool_input.get("pattern", "")[:120],
            }
            if _recent_mcp_call(tool_calls, i, "search"):
                entry["classification"] = "verification"
                verifications.append(entry)
            else:
                entry["classification"] = "miss"
                misses.append(entry)

        if name == "Agent":
            entry = {
                "type": "agent_miss",
                "tool_used": "Agent",
                "detail": tool_input.get("description", "")[:120],
            }
            if mcp_called:
                entry["classification"] = "verification"
                verifications.append(entry)
            else:
                entry["classification"] = "miss"
                misses.append(entry)

        if name == "Read":
            read_count += 1

    if "graph" in capabilities and read_count >= 5:
        has_graph_call = any(
            tc["name"].startswith("mcp__") and "graph" in tc["name"].lower()
            for tc in tool_calls
        )
        entry = {
            "type": "graph_miss",
            "tool_used": "Read",
            "detail": f"{read_count} file reads",
        }
        if has_graph_call:
            entry["classification"] = "verification"
            verifications.append(entry)
        else:
            entry["classification"] = "miss"
            misses.append(entry)

    by_type = {}
    for m in misses:
        by_type[m["type"]] = by_type.get(m["type"], 0) + 1

    return {
        "total": len(misses),
        "misses": misses,
        "verifications": verifications,
        "by_type": by_type,
    }


def score_keyword_presence(text, ground_truth, match_key):
    """Score qualitative response by keyword presence."""
    keywords = ground_truth.get(match_key, [])
    if not isinstance(keywords, list):
        keywords = []

    text_lower = text.lower()
    found = [k for k in keywords if k.lower() in text_lower]
    missing = [k for k in keywords if k.lower() not in text_lower]
    score = len(found) / len(keywords) if keywords else 0.0

    return {
        "type": "keyword_presence",
        "score": round(score, 4),
        "found_count": len(found),
        "expected_count": len(keywords),
        "found_keywords": found,
        "missing_keywords": missing,
    }


def score_result(result_dir, bench_dir, tool=None, repo=None, task=None):
    """Score a single benchmark result."""
    if not all([tool, repo, task]):
        path_parts = os.path.normpath(result_dir).split(os.sep)
        task = task or path_parts[-1]
        repo = repo or path_parts[-2]
        tool = tool or path_parts[-3]

    transcript_path = os.path.join(result_dir, "transcript.json")
    if not os.path.exists(transcript_path):
        return {"error": "no transcript", "tool": tool, "repo": repo, "task": task}

    run_meta_path = os.path.join(result_dir, "run_meta.json")
    run_meta = {}
    if os.path.exists(run_meta_path):
        with open(run_meta_path) as f:
            run_meta = json.load(f)

    index_meta_path = os.path.join(result_dir, "index_meta.json")
    index_meta = {}
    if os.path.exists(index_meta_path):
        with open(index_meta_path) as f:
            index_meta = json.load(f)

    transcript = parse_transcript(transcript_path)
    tool_stats = classify_tool_calls(transcript["tool_calls"])

    task_yaml_path = os.path.join(bench_dir, "tasks", task + ".yaml")
    lib_dir = os.path.join(bench_dir, "lib")
    sys.path.insert(0, lib_dir)
    from parse_task import parse_task
    task_config = parse_task(task_yaml_path)

    scoring_config = task_config.get("scoring", {})
    scoring_type = scoring_config.get("type", "")
    match_key = scoring_config.get("match_key", "")

    gt_file = task_config.get("repos", {}).get(repo, {}).get("ground_truth_file", "")
    gt_path = os.path.join(bench_dir, gt_file) if gt_file else ""

    ground_truth = {}
    gt_status = "missing"
    if gt_path and os.path.exists(gt_path):
        with open(gt_path) as f:
            ground_truth = json.load(f)
        gt_status = ground_truth.get("status", "unknown")

    if scoring_type == "set_match" and gt_status != "stub":
        response_json = extract_json_from_text(transcript["final_text"])
        correctness = score_set_match(response_json, ground_truth, match_key)
        if response_json is None:
            correctness["parse_error"] = True
    elif scoring_type == "qualitative" and gt_status != "stub":
        correctness = score_keyword_presence(
            transcript["final_text"], ground_truth, match_key
        )
    else:
        correctness = {"type": "skipped", "reason": f"gt_status={gt_status}"}

    wall_time = run_meta.get("wall_time_seconds")
    if wall_time is None and transcript["duration_ms"] is not None:
        wall_time = transcript["duration_ms"] / 1000.0

    miss_result = detect_misses(transcript["tool_calls"], tool)

    scored = {
        "tool": tool,
        "repo": repo,
        "task": task,
        "metrics": {
            "tool_calls": tool_stats["total"],
            "mcp_calls": tool_stats["mcp_calls"],
            "builtin_calls": tool_stats["builtin_calls"],
            "tool_call_types": {
                **tool_stats["mcp_tools"],
                **tool_stats["builtin_tools"],
            },
            "token_input": transcript["usage"]["input_tokens"],
            "token_output": transcript["usage"]["output_tokens"],
            "cache_read_input_tokens": transcript["usage"][
                "cache_read_input_tokens"
            ],
            "cache_creation_input_tokens": transcript["usage"][
                "cache_creation_input_tokens"
            ],
            "token_total": (
                transcript["usage"]["input_tokens"]
                + transcript["usage"]["cache_read_input_tokens"]
                + transcript["usage"]["cache_creation_input_tokens"]
                + transcript["usage"]["output_tokens"]
            ),
            "cost_usd": transcript["cost_usd"],
            "wall_time_seconds": wall_time,
            "duration_ms": transcript["duration_ms"],
            "num_turns": transcript["num_turns"],
            "index_completeness": index_meta,
        },
        "correctness": correctness,
        "misses": miss_result,
        "ground_truth_status": gt_status,
    }

    return scored


if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("Usage: scorer.py <result_dir> <bench_dir> [<tool> <repo> <task>]",
              file=sys.stderr)
        sys.exit(1)

    result_dir = sys.argv[1]
    bench_dir = sys.argv[2]
    tool = sys.argv[3] if len(sys.argv) > 3 else None
    repo = sys.argv[4] if len(sys.argv) > 4 else None
    task = sys.argv[5] if len(sys.argv) > 5 else None

    scored = score_result(result_dir, bench_dir, tool=tool, repo=repo, task=task)
    print(json.dumps(scored, indent=2))
