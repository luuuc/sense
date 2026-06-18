#!/usr/bin/env python3
"""Normalize a `codex exec --json` JSONL stream into the canonical Claude
stream-json transcript that bench/lib/scorer.py already consumes.

Codex and Claude Code emit different event streams, but the scorer only needs
three things from a transcript: tool calls (by name), assistant text, and token
usage. This converter maps Codex's typed items onto the Claude shapes
scorer.parse_transcript() / read_transcript_texts() read, so score.sh /
judge.sh / report.sh run on a Codex arm unchanged.

Mapping (Codex `item.completed` -> Claude stream-json events):
  mcp_tool_call{server,tool,arguments,result}
      -> assistant tool_use name="mcp__<server>__<tool>" input=arguments
       + user tool_result content=result.content
  command_execution{command,aggregated_output}
      -> assistant tool_use name="Bash" input={"command": ...}
       + user tool_result content=aggregated_output
  agent_message{text}
      -> assistant text block
  turn.completed{usage}
      -> accumulated into one final `result` event

Token mapping (Codex usage -> Claude usage): Codex `input_tokens` is the TOTAL
input (cached + uncached); Claude's `input_tokens` is the UNCACHED part, so:
  cache_read_input_tokens     = cached_input_tokens
  input_tokens (uncached)     = input_tokens - cached_input_tokens
  output_tokens               = output_tokens + reasoning_output_tokens
  cache_creation_input_tokens = 0  (Codex does not report it)
Cost is left null: Codex runs on a subscription, so a per-token cost is not
meaningful (same stance as the Claude/Ollama subscription arms).

Also emits a channel summary to stderr (and to --channels-json FILE): Sense
reaches Codex through two channels — the MCP server and the `sense` CLI on PATH
— and we measure which the model prefers. MCP calls are counted from
mcp_tool_call(server=sense); CLI calls from command_execution that invoke the
`sense` binary.

Usage:
  parse-codex-result.py codex-raw.jsonl > transcript.json
  parse-codex-result.py codex-raw.jsonl --channels-json channels.json > transcript.json
"""
import json
import re
import sys

SENSE_CLI = re.compile(r"\bsense\b")
MANUAL = re.compile(r"\b(rg|grep|cat|sed|find|ls|head|tail|awk|fd)\b")


def emit(obj):
    sys.stdout.write(json.dumps(obj) + "\n")


def assistant(content):
    return {"type": "assistant", "message": {"content": content}}


def user_tool_result(content):
    return {"type": "user",
            "message": {"content": [{"type": "tool_result", "content": content}]}}


def main():
    args = sys.argv[1:]
    channels_path = None
    if "--channels-json" in args:
        i = args.index("--channels-json")
        channels_path = args[i + 1]
        del args[i:i + 2]
    if not args:
        sys.exit("usage: parse-codex-result.py codex-raw.jsonl "
                 "[--channels-json FILE] > transcript.json")
    src = args[0]

    usage = {"input_tokens": 0, "output_tokens": 0,
             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}
    session_id = None
    channels = {"mcp_sense": 0, "cli_sense": 0, "manual": 0, "other_shell": 0}

    with open(src) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue

            etype = e.get("type", "")

            if etype == "thread.started":
                session_id = e.get("thread_id")
                continue

            if etype == "turn.completed":
                u = e.get("usage", {}) or {}
                total_in = u.get("input_tokens", 0) or 0
                cached = u.get("cached_input_tokens", 0) or 0
                usage["cache_read_input_tokens"] += cached
                usage["input_tokens"] += max(0, total_in - cached)
                usage["output_tokens"] += ((u.get("output_tokens", 0) or 0)
                                           + (u.get("reasoning_output_tokens", 0) or 0))
                continue

            if etype != "item.completed":
                continue
            item = e.get("item", {}) or {}
            itype = item.get("type")

            if itype == "agent_message":
                text = item.get("text", "")
                if text:
                    emit(assistant([{"type": "text", "text": text}]))

            elif itype == "mcp_tool_call":
                server = item.get("server", "")
                tool = item.get("tool", "")
                emit(assistant([{"type": "tool_use",
                                 "name": f"mcp__{server}__{tool}",
                                 "input": item.get("arguments", {}) or {}}]))
                result = item.get("result", {}) or {}
                emit(user_tool_result(result.get("content", []) or []))
                if server == "sense":
                    channels["mcp_sense"] += 1

            elif itype == "command_execution":
                cmd = item.get("command", "")
                emit(assistant([{"type": "tool_use", "name": "Bash",
                                 "input": {"command": cmd}}]))
                emit(user_tool_result(
                    [{"type": "text", "text": item.get("aggregated_output", "")}]))
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
