#!/usr/bin/env python3
"""Normalize an `opencode run --format json` JSONL stream into the canonical
Claude stream-json transcript that bench/lib/scorer.py already consumes.

opencode emits one JSON object per line, each wrapping a session "part":
  {"type":"text","sessionID":..,"part":{"type":"text","id":..,"text":..}}
  {"type":"tool","sessionID":..,"part":{"type":"tool","id":..,"tool":"read",
        "callID":..,"state":{"status":"completed","input":{..},"output":".."}}}
  {"type":"step_finish","part":{"type":"step-finish","tokens":{..},"cost":..}}
The scorer only needs tool calls (by name), assistant text, and token usage, so
this converter maps opencode parts onto the Claude shapes
scorer.parse_transcript() / read_transcript_texts() read. score.sh / judge.sh /
report.sh then runs on an opencode arm unchanged.

Mapping (opencode part -> Claude stream-json events):
  part.type "text"  -> assistant text block
  part.type "tool"  -> assistant tool_use (name remapped, see below)
                     + user tool_result content=state.output
  part.type "step-finish" -> tokens accumulated into one final `result` event
  part.type "step-start" / "reasoning" / "patch" -> ignored

Tool-name remap (opencode -> Claude, so no_grep + native counts line up):
  read->Read, grep->Grep, glob->Glob, list->LS, bash->Bash, webfetch->WebFetch,
  edit->Edit, write->Write; a Sense MCP tool (sense_graph / sense_search /
  sense_blast / sense_conventions / sense_status, however the server prefixes it)
  -> mcp__sense__sense_<verb>; anything else passes through unchanged.

Token mapping (opencode tokens -> Claude usage). opencode's per-step `input` is
the UNCACHED input (total = input + output + cache.read), matching Claude's
`input_tokens` semantics, so:
  input_tokens (uncached)     = sum(input)
  cache_read_input_tokens     = sum(cache.read)
  cache_creation_input_tokens = sum(cache.write)
  output_tokens               = sum(output + reasoning)
Steps are summed the same way the Claude scorer sums usage across result events.
Cost is left null: opencode/ollama-cloud bills off-platform (same stance as the
Codex/Ollama subscription arms).

Also emits a channel summary to stderr (and to --channels-json FILE): Sense
reaches opencode through two channels, the MCP server and the `sense` CLI on
PATH, and we measure which the model prefers. MCP calls are counted from tool
parts that remap to mcp__sense__*; CLI calls from bash tool parts that invoke
the `sense` binary.

Usage:
  parse-opencode-result.py opencode-raw.jsonl > transcript.json
  parse-opencode-result.py opencode-raw.jsonl --channels-json channels.json > transcript.json
"""
import json
import re
import sys

SENSE_VERB = re.compile(r"(graph|search|blast|conventions|status)$")
SENSE_CLI = re.compile(r"\bsense\b")
MANUAL = re.compile(r"\b(rg|grep|cat|sed|find|ls|head|tail|awk|fd)\b")

NATIVE_REMAP = {
    "read": "Read", "grep": "Grep", "glob": "Glob", "list": "LS",
    "bash": "Bash", "webfetch": "WebFetch", "edit": "Edit", "write": "Write",
}


def emit(obj):
    sys.stdout.write(json.dumps(obj) + "\n")


def assistant(content):
    return {"type": "assistant", "message": {"content": content}}


def user_tool_result(content):
    return {"type": "user",
            "message": {"content": [{"type": "tool_result", "content": content}]}}


def remap_tool(name):
    """opencode tool name -> (claude_name, is_sense_mcp)."""
    low = (name or "").lower()
    if low.startswith("sense"):
        m = SENSE_VERB.search(low)
        if m:
            return f"mcp__sense__sense_{m.group(1)}", True
    return NATIVE_REMAP.get(low, name), False


def main():
    args = sys.argv[1:]
    channels_path = None
    if "--channels-json" in args:
        i = args.index("--channels-json")
        channels_path = args[i + 1]
        del args[i:i + 2]
    if not args:
        sys.exit("usage: parse-opencode-result.py opencode-raw.jsonl "
                 "[--channels-json FILE] > transcript.json")
    src = args[0]

    usage = {"input_tokens": 0, "output_tokens": 0,
             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}
    session_id = None
    channels = {"mcp_sense": 0, "cli_sense": 0, "manual": 0, "other_shell": 0}

    # opencode re-emits the same part id as its state streams (pending -> running
    # -> completed), so collapse to the latest version of each part, preserving
    # first-seen order. step-finish parts have no stable id we need; fold inline.
    parts = {}
    order = []

    with open(src) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue

            sid = e.get("sessionID")
            if sid:
                session_id = sid

            p = e.get("part") or {}
            ptype = p.get("type")

            if ptype == "step-finish":
                t = p.get("tokens") or {}
                cache = t.get("cache") or {}
                usage["input_tokens"] += t.get("input", 0) or 0
                usage["output_tokens"] += ((t.get("output", 0) or 0)
                                           + (t.get("reasoning", 0) or 0))
                usage["cache_read_input_tokens"] += cache.get("read", 0) or 0
                usage["cache_creation_input_tokens"] += cache.get("write", 0) or 0
                continue

            if ptype in ("step-start", "patch"):
                continue

            pid = p.get("id")
            if pid is None:
                continue
            if pid not in parts:
                order.append(pid)
            parts[pid] = p

    for pid in order:
        p = parts[pid]
        ptype = p.get("type")

        if ptype == "text":
            text = p.get("text", "")
            if text:
                emit(assistant([{"type": "text", "text": text}]))

        elif ptype == "reasoning":
            continue  # not scored; drop to keep the transcript lean

        elif ptype == "tool":
            name, is_sense = remap_tool(p.get("tool", ""))
            state = p.get("state", {}) or {}
            inp = state.get("input", {}) or {}
            emit(assistant([{"type": "tool_use", "name": name, "input": inp}]))
            out = state.get("output", "")
            emit(user_tool_result(
                out if isinstance(out, list)
                else [{"type": "text", "text": str(out)}]))
            if is_sense:
                channels["mcp_sense"] += 1
            elif name == "Bash":
                cmd = str(inp.get("command", ""))
                if SENSE_CLI.search(cmd):
                    channels["cli_sense"] += 1
                elif MANUAL.search(cmd):
                    channels["manual"] += 1
                else:
                    channels["other_shell"] += 1

    emit({"type": "result", "total_cost_usd": None, "duration_ms": None,
          "usage": usage, "session_id": session_id})

    sense_total = channels["mcp_sense"] + channels["cli_sense"]
    if channels["mcp_sense"] > channels["cli_sense"]:
        preferred = "mcp"
    elif channels["cli_sense"] > channels["mcp_sense"]:
        preferred = "cli"
    else:
        preferred = "tie/none"
    summary = {"channels": channels, "sense_total": sense_total,
               "preferred": preferred}
    sys.stderr.write("channel summary: " + json.dumps(summary) + "\n")
    if channels_path:
        with open(channels_path, "w") as cf:
            json.dump(summary, cf, indent=2)


if __name__ == "__main__":
    main()
