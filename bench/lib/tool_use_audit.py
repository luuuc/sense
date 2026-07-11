#!/usr/bin/env python3
"""Misuse audit over captured Sense MCP traffic (goal-file sensory system 2).

Reads a run's sense-io.jsonl (written by mcp_tee.py) — or, degraded, a
transcript.json from a pre-capture run — normalizes it into an ordered list
of Sense tool calls with their FULL responses, and flags misuse patterns.
A flag is a hypothesis for the evaluator/human, never a verdict; every flag
carries its evidence and names the product meta-surface whose fix would
prevent it (tool-contract / response-shape / setup). The scorer never reads
this file (misuse findings fix surfaces upstream, never scoring).

Detectors (each anchored to a real precedent):
  contract-misled     first call for a symbol uses a non-default confidence
                      with no default-confidence call for the same symbol
                      anywhere in the session → the schema, not the task,
                      chose the param (the blast min_confidence 0.7-vs-0.3
                      contract bug). Deliberate narrowing = a default call
                      for the same symbol exists → NOT flagged.
  ignored-hint        a graph/blast response says low-confidence results were
                      hidden (low_confidence_hidden > 0) while returning few/no
                      edges, and no follow-up call for the same symbol lowers
                      min_confidence → the documented re-run advice was missed.
  abandoned-on-empty  an empty result for a target and the session never
                      retries that target with ANY adjusted Sense call
                      (different tool, lower confidence, file disambiguator).
  wrong-tool-shape    sense_graph/sense_blast called with natural-language
                      text as the symbol (concept questions belong to
                      sense_search); conservative: ≥3 words or no
                      identifier-like token.

--coverage mode: per-surface exercise report (calls, empties, errors, bytes,
params seen) — the feature-coverage check; unexercised surface = unimproved
surface.

Usage:
  tool_use_audit.py <sense-io.jsonl> [--json]
  tool_use_audit.py --from-transcript <transcript.json> [--json]
  tool_use_audit.py <input> --coverage [--json]
"""

import argparse
import json
import re
import sys

SENSE_TOOLS = ("sense_graph", "sense_blast", "sense_search",
               "sense_conventions", "sense_status")

DEFAULT_MIN_CONF = {"sense_graph": 0.5, "sense_blast": 0.3}

IDENTIFIER = re.compile(r"^[\w.:$/\\-]+$")


class Call:
    def __init__(self, index, tool, args, result_text, is_error):
        self.index = index
        self.tool = tool
        self.args = args or {}
        self.result_text = result_text or ""
        self.is_error = is_error
        try:
            self.result = json.loads(self.result_text)
        except ValueError:
            self.result = None

    @property
    def target(self):
        return self.args.get("symbol") or self.args.get("query") or ""

    @property
    def empty(self):
        """Best-effort 'returned nothing useful' per the real mcpio schemas:
        graph nests its lists under "edges" (types.go GraphEdges) with
        dispatch_inferred alongside; blast and search are top-level."""
        r = self.result
        if r is None or not isinstance(r, dict):
            return False
        if self.tool == "sense_graph":
            edges = r.get("edges") or {}
            return not any(edges.get(k) for k in
                           ("calls", "called_by", "inherits", "inherited_by",
                            "composes", "composed_by", "includes", "imports")) \
                and not r.get("dispatch_inferred")
        if self.tool == "sense_blast":
            return (r.get("total_affected") or 0) == 0 \
                and not r.get("direct_callers")
        if self.tool == "sense_search":
            return not r.get("results")
        return False

    @property
    def hidden_hint(self):
        r = self.result
        if isinstance(r, dict):
            return r.get("low_confidence_hidden") or 0
        return 0


# --- loaders -----------------------------------------------------------------

def load_sense_io(path):
    """Pair tools/call requests with responses by JSON-RPC id."""
    reqs, resps = {}, {}
    order = []
    for line in open(path, encoding="utf-8"):
        line = line.strip()
        if not line:
            continue
        frame = json.loads(line)
        msg = frame.get("msg") or {}
        if not isinstance(msg, dict):
            continue
        if frame.get("dir") == "c2s" and msg.get("method") == "tools/call":
            rid = msg.get("id")
            reqs[rid] = msg.get("params") or {}
            order.append(rid)
        elif frame.get("dir") == "s2c" and msg.get("id") in reqs:
            resps[msg["id"]] = msg.get("result") or {}
    calls = []
    for rid in order:
        params = reqs[rid]
        tool = (params.get("name") or "").replace("mcp__sense__", "")
        if tool not in SENSE_TOOLS:
            continue
        result = resps.get(rid, {})
        content = result.get("content") or []
        text = content[0].get("text", "") if content else ""
        calls.append(Call(len(calls), tool, params.get("arguments"),
                          text, bool(result.get("isError"))))
    return calls


def load_transcript(path):
    """Degraded fallback for pre-capture runs: tool_use/tool_result blocks in a
    Claude-style transcript (stream-json events or a JSON array of messages).
    Responses may be truncated by the harness — flags from this mode carry
    lower confidence and coverage byte-counts are lower bounds."""
    raw = open(path, encoding="utf-8").read().strip()
    events = []
    if raw.startswith("["):
        events = json.loads(raw)
    else:
        for line in raw.splitlines():
            line = line.strip()
            if line:
                try:
                    events.append(json.loads(line))
                except ValueError:
                    continue
    uses, results = {}, {}
    order = []
    def walk(obj):
        if isinstance(obj, dict):
            if obj.get("type") == "tool_use" and "sense" in (obj.get("name") or ""):
                uses[obj.get("id")] = obj
                order.append(obj.get("id"))
            elif obj.get("type") == "tool_result" and obj.get("tool_use_id") in uses:
                results[obj["tool_use_id"]] = obj
            for v in obj.values():
                walk(v)
        elif isinstance(obj, list):
            for v in obj:
                walk(v)
    walk(events)
    calls = []
    for uid in order:
        u = uses[uid]
        tool = (u.get("name") or "").replace("mcp__sense__", "")
        if tool not in SENSE_TOOLS:
            continue
        res = results.get(uid, {})
        content = res.get("content")
        if isinstance(content, list):
            text = "".join(c.get("text", "") for c in content
                           if isinstance(c, dict))
        else:
            text = content if isinstance(content, str) else ""
        calls.append(Call(len(calls), tool, u.get("input"),
                          text, bool(res.get("is_error"))))
    return calls


# --- detectors ---------------------------------------------------------------

def flag(detector, surface, call, evidence, lever):
    return {"detector": detector, "surface": surface, "call": call.index,
            "tool": call.tool, "target": call.target,
            "evidence": evidence, "lever": lever}


def detect_contract_misled(calls):
    out = []
    for c in calls:
        mc = c.args.get("min_confidence")
        if mc is None or c.tool not in DEFAULT_MIN_CONF:
            continue
        if mc <= DEFAULT_MIN_CONF[c.tool]:
            continue
        same_symbol_default = any(
            o.tool == c.tool and o.target == c.target
            and o.args.get("min_confidence") is None
            for o in calls)
        if not same_symbol_default:
            out.append(flag(
                "contract-misled", "tool-contract", c,
                f"min_confidence={mc} on the only {c.tool} call for "
                f"'{c.target}' (default {DEFAULT_MIN_CONF[c.tool]}); no "
                f"default-confidence call for this symbol in the session — "
                f"the schema, not the task, chose the param",
                "audit the tool description/schema default for this param"))
    return out


def detect_ignored_hint(calls):
    out = []
    for c in calls:
        if c.tool != "sense_graph" or c.hidden_hint <= 0 or not c.empty:
            continue
        retried_lower = any(
            o.index > c.index and o.tool == c.tool and o.target == c.target
            and (o.args.get("min_confidence") or 1) < (c.args.get("min_confidence") or 0.5)
            for o in calls)
        if not retried_lower:
            out.append(flag(
                "ignored-hint", "response-shape", c,
                f"empty edges with low_confidence_hidden={c.hidden_hint} and "
                f"no lower-confidence re-run for '{c.target}' — the "
                f"documented re-run advice did not land",
                "make the hint actionable in the response text (or auto-widen)"))
    return out


def detect_abandoned_on_empty(calls):
    out = []
    for c in calls:
        if not c.empty or not c.target:
            continue
        adjusted = any(
            o.index > c.index and o.target and
            (o.target == c.target or c.target in o.target or o.target in c.target)
            for o in calls)
        if not adjusted:
            out.append(flag(
                "abandoned-on-empty", "response-shape", c,
                f"{c.tool} returned empty for '{c.target}' and the session "
                f"never retried the target with any adjusted Sense call",
                "empty-result guidance: say WHAT to try next in the response"))
    return out


def detect_wrong_tool_shape(calls):
    out = []
    for c in calls:
        if c.tool not in ("sense_graph", "sense_blast"):
            continue
        sym = c.args.get("symbol") or ""
        if not sym:
            continue
        words = sym.split()
        if len(words) >= 3 or (words and not any(IDENTIFIER.match(w) for w in words)):
            out.append(flag(
                "wrong-tool-shape", "tool-contract", c,
                f"natural-language symbol '{sym}' passed to {c.tool} — "
                f"concept questions belong to sense_search",
                "sharpen the symbol-vs-concept steer in the tool descriptions"))
    return out


DETECTORS = (detect_contract_misled, detect_ignored_hint,
             detect_abandoned_on_empty, detect_wrong_tool_shape)


# --- coverage ----------------------------------------------------------------

def coverage(calls):
    cov = {t: {"calls": 0, "empty": 0, "errors": 0, "response_bytes": 0,
               "params_seen": {}} for t in SENSE_TOOLS}
    for c in calls:
        row = cov[c.tool]
        row["calls"] += 1
        row["empty"] += int(c.empty)
        row["errors"] += int(c.is_error)
        row["response_bytes"] += len(c.result_text)
        for k, v in sorted(c.args.items()):
            if k in ("symbol", "query"):
                continue
            row["params_seen"].setdefault(k, [])
            if v not in row["params_seen"][k]:
                row["params_seen"][k].append(v)
    cov["_unexercised"] = [t for t in SENSE_TOOLS if cov[t]["calls"] == 0]
    return cov


# --- main --------------------------------------------------------------------

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("input", nargs="?", help="sense-io.jsonl from mcp_tee.py")
    ap.add_argument("--from-transcript", help="degraded mode: pre-capture transcript.json")
    ap.add_argument("--coverage", action="store_true",
                    help="per-surface exercise report instead of misuse flags")
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()

    if args.from_transcript:
        calls = load_transcript(args.from_transcript)
        mode = "transcript (degraded: responses may be truncated)"
    elif args.input:
        calls = load_sense_io(args.input)
        mode = "sense-io"
    else:
        ap.error("need a sense-io.jsonl or --from-transcript")

    if args.coverage:
        report = {"mode": mode, "calls": len(calls), "coverage": coverage(calls)}
        print(json.dumps(report, indent=2) if args.json else
              format_coverage(report))
        return 0

    flags = [f for det in DETECTORS for f in det(calls)]
    report = {"mode": mode, "calls": len(calls), "flags": flags}
    if args.json:
        print(json.dumps(report, indent=2))
    else:
        print(f"# tool_use_audit — {len(calls)} sense calls ({mode})")
        if not flags:
            print("no misuse flags")
        for f in flags:
            print(f"[{f['detector']}] call {f['call']} {f['tool']}"
                  f"({f['target']}) surface={f['surface']}")
            print(f"    {f['evidence']}")
            print(f"    lever: {f['lever']}")
    return 0


def format_coverage(report):
    lines = [f"# surface coverage — {report['calls']} sense calls ({report['mode']})"]
    for tool, row in report["coverage"].items():
        if tool == "_unexercised":
            continue
        lines.append(f"{tool}: calls={row['calls']} empty={row['empty']} "
                     f"errors={row['errors']} bytes={row['response_bytes']} "
                     f"params={row['params_seen'] or '{}'}")
    un = report["coverage"]["_unexercised"]
    lines.append(f"unexercised surfaces: {', '.join(un) if un else 'none'}")
    return "\n".join(lines)


if __name__ == "__main__":
    sys.exit(main())
