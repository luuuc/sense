#!/usr/bin/env python3
"""Acceptance test for mcp_tee.py: transparency + capture + fail-open.

Run from the repo root (needs a built binary and the repo's own .sense index):
    python3 bench/lib/test_mcp_tee.py

Asserts the shim's contract:
  1. Transparency: a scripted MCP session through the shim is byte-identical
     to a direct session after masking the embedded "freshness":{...} object
     (wall-clock ages, last_update, stale_files_seen drift between ANY two
     sessions, especially while the watcher is absorbing recent edits;
     verified 2026-07-11 that direct-vs-direct differs the same way).
  2. Capture: the log holds every frame, both directions, all parsed.
  3. Fail-open: with no --log and no $SENSE_IO_LOG the shim execs the server
     directly and the session still works.
"""

import json
import os
import re
import subprocess
import sys
import tempfile

SESSION = "\n".join([
    json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
        "protocolVersion": "2024-11-05", "capabilities": {},
        "clientInfo": {"name": "tee-fixture", "version": "0"}}}),
    json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}),
    json.dumps({"jsonrpc": "2.0", "id": 2, "method": "tools/list"}),
    json.dumps({"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {
        "name": "sense_graph", "arguments": {"symbol": "ApplyBlastBudget"}}}),
]) + "\n"

# The freshness block is serialized inside the tool-result text payload with
# escaped quotes; mask it wherever it appears, escaped or not. It contains no
# structural data (ages, timestamps, stale-file counters only).
VOLATILE = re.compile(r'(\\?"freshness\\?":)\{[^{}]*\}')


def mask(raw: bytes) -> bytes:
    return VOLATILE.sub(r"\1{}", raw.decode("utf-8")).encode("utf-8")


def run(cmd, env=None):
    e = dict(os.environ)
    e.pop("SENSE_IO_LOG", None)
    if env:
        e.update(env)
    p = subprocess.run(cmd, input=SESSION.encode(), capture_output=True, env=e)
    return p.stdout


def main():
    sense = "./bin/sense" if os.path.exists("./bin/sense") else "sense"
    shim = "bench/lib/mcp_tee.py"
    fails = []

    with tempfile.TemporaryDirectory() as td:
        log = os.path.join(td, "sense-io.jsonl")
        direct = run([sense, "mcp"])
        teed = run([sys.executable, shim, "--log", log, "--", sense, "mcp"])

        if not direct.strip():
            print("FAIL: direct session produced no output (index/binary missing?)")
            return 1
        if mask(direct) != mask(teed):
            fails.append("transparency: teed output differs from direct beyond freshness fields")

        frames = [json.loads(l) for l in open(log)]
        c2s = [f for f in frames if f["dir"] == "c2s"]
        s2c = [f for f in frames if f["dir"] == "s2c"]
        if len(c2s) != 4:
            fails.append(f"capture: expected 4 c2s frames, got {len(c2s)}")
        if len(s2c) != 3:
            fails.append(f"capture: expected 3 s2c frames, got {len(s2c)}")
        if any("_unparsed" in f["msg"] for f in frames if isinstance(f["msg"], dict)):
            fails.append("capture: unparsed frames in log")
        calls = [f for f in s2c if f["msg"].get("id") == 3]
        if not calls or "result" not in calls[0]["msg"]:
            fails.append("capture: tools/call full response missing from log")

        passthrough = run([sys.executable, shim, "--", sense, "mcp"])
        if mask(passthrough) != mask(direct):
            fails.append("fail-open: passthrough mode (no log) altered the session")

    for f in fails:
        print("FAIL:", f)
    print("mcp_tee:", "FAIL" if fails else "PASS", "(transparency, capture, fail-open)")
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
