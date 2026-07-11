#!/usr/bin/env python3
"""Fixture test for tool_use_audit.py (Loop 3 branch-3b acceptance, sense-io side).

    python3 bench/lib/test_tool_use_audit.py

Fixtures are built in-memory as mcp_tee.py-format frames:
  1. contract-bug replay: cold blast at min_confidence=0.7 (the schema's
     misleading claimed default) with no default-confidence call for the
     symbol → MUST flag contract-misled on the tool-contract surface.
  2. deliberate narrowing: default blast first, THEN 0.7 for the same symbol
     → MUST NOT flag (misled vs intentional is the discriminator; an audit
     that cannot tell them apart is over-tuned).
  3. ignored-hint: graph empty with low_confidence_hidden=3, never re-run
     lower → MUST flag ignored-hint.
  4. abandoned-on-empty: search returns nothing, target never retried → MUST
     flag abandoned-on-empty.
  5. clean session (call, non-empty result, follow-ups) → zero flags
     (negative control, same law as the evaluator's sentry fixture).
"""

import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import tool_use_audit as tua  # noqa: E402


def frames(*calls):
    """Build sense-io.jsonl lines from (tool, args, result_dict) triples."""
    out = []
    for i, (tool, args, result) in enumerate(calls, start=10):
        out.append(json.dumps({"ts": "T", "dir": "c2s", "msg": {
            "jsonrpc": "2.0", "id": i, "method": "tools/call",
            "params": {"name": tool, "arguments": args}}}))
        out.append(json.dumps({"ts": "T", "dir": "s2c", "msg": {
            "jsonrpc": "2.0", "id": i, "result": {
                "content": [{"type": "text", "text": json.dumps(result)}]}}}))
    return "\n".join(out) + "\n"


# Shapes mirror the real mcpio schemas (types.go): graph edge lists nest
# under "edges"; blast/search fields are top-level.
GRAPH_OK = {"symbol": {"name": "X"},
            "edges": {"called_by": [{"symbol": "Y"}], "calls": []}}
GRAPH_EMPTY_HIDDEN = {"symbol": {"name": "X"},
                      "edges": {"called_by": [], "calls": []},
                      "low_confidence_hidden": 3}
BLAST_OK = {"symbol": {"name": "Device"}, "direct_callers": [{"symbol": "Z"}],
            "total_affected": 12}
SEARCH_EMPTY = {"results": []}


def run_audit(text):
    with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as f:
        f.write(text)
        path = f.name
    try:
        calls = tua.load_sense_io(path)
        return [fl for det in tua.DETECTORS for fl in det(calls)], calls
    finally:
        os.unlink(path)


def main():
    fails = []

    # 1. contract-bug replay → contract-misled
    flags, _ = run_audit(frames(
        ("sense_blast", {"symbol": "Device", "min_confidence": 0.7}, BLAST_OK)))
    hits = [f for f in flags if f["detector"] == "contract-misled"]
    if not (hits and hits[0]["surface"] == "tool-contract"):
        fails.append("fixture 1: contract-bug replay did not flag contract-misled/tool-contract")

    # 2. deliberate narrowing → silent
    flags, _ = run_audit(frames(
        ("sense_blast", {"symbol": "Device"}, BLAST_OK),
        ("sense_blast", {"symbol": "Device", "min_confidence": 0.7}, BLAST_OK)))
    if any(f["detector"] == "contract-misled" for f in flags):
        fails.append("fixture 2: deliberate narrowing was flagged (over-tuned)")

    # 3. ignored-hint
    flags, _ = run_audit(frames(
        ("sense_graph", {"symbol": "Handler"}, GRAPH_EMPTY_HIDDEN),
        ("sense_search", {"query": "unrelated concept"}, {"results": [{"s": 1}]})))
    if not any(f["detector"] == "ignored-hint" for f in flags):
        fails.append("fixture 3: hidden-hint empty graph not flagged")

    # 3b. hint acted on → silent
    flags, _ = run_audit(frames(
        ("sense_graph", {"symbol": "Handler"}, GRAPH_EMPTY_HIDDEN),
        ("sense_graph", {"symbol": "Handler", "min_confidence": 0.3}, GRAPH_OK)))
    if any(f["detector"] in ("ignored-hint", "abandoned-on-empty") for f in flags):
        fails.append("fixture 3b: acted-on hint still flagged (over-tuned)")

    # 4. abandoned-on-empty
    flags, _ = run_audit(frames(
        ("sense_search", {"query": "payment retry logic"}, SEARCH_EMPTY),
        ("sense_graph", {"symbol": "Other"}, GRAPH_OK)))
    if not any(f["detector"] == "abandoned-on-empty" for f in flags):
        fails.append("fixture 4: abandoned empty search not flagged")

    # 5. clean session → zero flags
    flags, calls = run_audit(frames(
        ("sense_status", {}, {"files": 100}),
        ("sense_graph", {"symbol": "Device"}, GRAPH_OK),
        ("sense_blast", {"symbol": "Device"}, BLAST_OK)))
    if flags:
        fails.append(f"fixture 5: clean session produced flags: "
                     f"{[f['detector'] for f in flags]}")

    # coverage sanity on fixture 5's calls
    cov = tua.coverage(calls)
    if cov["sense_graph"]["calls"] != 1 or "sense_search" not in cov["_unexercised"]:
        fails.append("coverage: counts or unexercised list wrong")

    for f in fails:
        print("FAIL:", f)
    print("tool_use_audit:", "FAIL" if fails else "PASS",
          "(contract-misled +/-, ignored-hint +/-, abandoned, clean, coverage)")
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
