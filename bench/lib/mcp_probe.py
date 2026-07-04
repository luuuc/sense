#!/usr/bin/env python3
"""Drive `sense mcp` over stdio and print the agent-facing tool results.

The scenario-crafting rule this serves: gold must be verified retrievable in
the SHOWN budgeted output the AGENT sees — over MCP stdio, at BOTH
min_confidence 0.3 and 0.7 — because the CLI diverges by design and the
blast eviction runs both ways (0.7 drops low-confidence riders; 0.3 admits
more competitors to the caller cap and can evict 0.7-confidence gold).
Every scenario session was re-writing this probe in its scratchpad; now it
is one command (craft prompt step 4).

Usage:
  mcp_probe.py <repo_clone> '<calls_json>'

  <calls_json> is a JSON array of tool calls, e.g.:
    '[{"name":"sense_blast","arguments":{"symbol":"QuerySet.filter"}},
      {"name":"sense_blast","arguments":{"symbol":"QuerySet.filter","min_confidence":0.7}},
      {"name":"sense_graph","arguments":{"symbol":"QuerySet.filter","direction":"callers"}}]'

Each result prints as a `═══ call id=N ═══` header followed by the raw JSON
text the agent would receive — pipe into python/jq to assert gold presence.
SENSE_BIN overrides the binary (default: `sense` on PATH).
"""

import json
import os
import subprocess
import sys


def build_messages(calls):
    msgs = [
        {"jsonrpc": "2.0", "id": 0, "method": "initialize",
         "params": {"protocolVersion": "2024-11-05",
                    "capabilities": {},
                    "clientInfo": {"name": "mcp_probe", "version": "0"}}},
        {"jsonrpc": "2.0", "method": "notifications/initialized"},
    ]
    for i, c in enumerate(calls, start=1):
        msgs.append({"jsonrpc": "2.0", "id": i, "method": "tools/call",
                     "params": {"name": c["name"], "arguments": c.get("arguments", {})}})
    return msgs


def probe(repo, calls, sense_bin):
    inp = "\n".join(json.dumps(m) for m in build_messages(calls)) + "\n"
    proc = subprocess.run([sense_bin, "mcp"], input=inp, capture_output=True,
                          text=True, cwd=repo, timeout=120)
    results = []
    for line in proc.stdout.splitlines():
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if obj.get("id", 0) >= 1 and "result" in obj:
            for block in obj["result"].get("content", []):
                results.append((obj["id"], block.get("text", "")))
    return results, proc


def main():
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        sys.exit(1)
    repo, calls_json = sys.argv[1], sys.argv[2]
    if not os.path.isdir(os.path.join(repo, ".sense")):
        print(f"mcp_probe: no .sense index under {repo}", file=sys.stderr)
        sys.exit(1)
    calls = json.loads(calls_json)
    sense_bin = os.environ.get("SENSE_BIN", "sense")

    results, proc = probe(repo, calls, sense_bin)
    for call_id, text in results:
        print(f"═══ call id={call_id} ═══")
        print(text)
    if proc.returncode != 0:
        print("STDERR:", proc.stderr[-2000:], file=sys.stderr)
        sys.exit(proc.returncode)
    if not results:
        print("mcp_probe: no tool results returned", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
