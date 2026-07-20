#!/usr/bin/env python3
"""Axis panel: score every banked cell on ALL brainstorm axes, from disk.

Observational instrument for the beyond-invisibility axis work: runs
efficiency.py per cell and flattens its output into one row per cell so
every bench run feeds the multi-axis dataset (reach, cost, time, turnarounds,
navigation profile, reliability spread, grounding). The headline verdict stays
with the judging contract; panel rows are hypothesis fuel, never win claims.

Usage: panel.py [--json OUT.jsonl] [--md OUT.md]
Scans bench/verticals/*/results/claude-opus-4-8/{baseline,sense}/<repo>.
"""
import argparse
import json
import os
import re
import subprocess
import sys

LIB = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.normpath(os.path.join(LIB, "..", ".."))
VERTICALS = os.path.join(REPO_ROOT, "bench", "verticals")
MODEL = "claude-opus-4-8"

ROW = re.compile(r"^\| (?P<axis>[^|]+?) \| (?P<base>[^|]+?) \| (?P<sense>[^|]+?) \| (?P<delta>[^|]+?) \|$")
REACH = re.compile(r"cited_recall (?P<base>[\d.]+)→(?P<sense>[\d.]+) \((?P<mult>[\d.]+)×\)")
RELI = re.compile(r"baseline \[(?P<base>[^\]]+)\] → sense \[(?P<sense>[^\]]+)\]")


def cells():
    for vert in sorted(os.listdir(VERTICALS)):
        base = os.path.join(VERTICALS, vert, "results", MODEL, "baseline")
        if not os.path.isdir(base):
            continue
        for repo in sorted(os.listdir(base)):
            if os.path.isdir(os.path.join(base, repo)):
                yield vert, repo


def spread(text):
    vals = [int(x) for x in text.replace(" ", "").split(",") if x]
    return {"runs": vals, "spread": max(vals) - min(vals) if vals else None}


def parse(vert, repo, out):
    row = {"vertical": vert, "repo": repo, "model": MODEL}
    for line in out.splitlines():
        m = ROW.match(line.strip())
        if m:
            axis = m.group("axis").strip()
            key = {
                "billed tokens": "tokens",
                "session time (s, median)": "wall_s",
                "cost ($)": "cost_usd",
                "turnarounds (tool calls)": "tool_calls",
                "navigation (grep / mcp / read)": "nav",
                "grounding (anti-fab)": "grounding",
            }.get(axis)
            if key:
                row[key] = {"baseline": m.group("base").strip(),
                            "sense": m.group("sense").strip(),
                            "delta": m.group("delta").strip()}
        m = REACH.search(line)
        if m:
            row["cited_recall"] = {"baseline": float(m.group("base")),
                                   "sense": float(m.group("sense")),
                                   "mult": float(m.group("mult"))}
        if "reliability" in line:
            m = RELI.search(line)
            if m:
                row["reliability"] = {"baseline": spread(m.group("base")),
                                      "sense": spread(m.group("sense"))}
    return row


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", default=os.path.join(REPO_ROOT, "bench", "results", "panel", "panel.jsonl"))
    ap.add_argument("--md", default=os.path.join(REPO_ROOT, "bench", "results", "panel", "panel.md"))
    args = ap.parse_args()
    os.makedirs(os.path.dirname(args.json), exist_ok=True)

    rows = []
    for vert, repo in cells():
        results_dir = os.path.join(VERTICALS, vert, "results", MODEL)
        proc = subprocess.run([sys.executable, os.path.join(LIB, "efficiency.py"), repo, results_dir],
                              capture_output=True, text=True)
        if proc.returncode != 0:
            print(f"skip {vert}/{repo}: {proc.stderr.strip().splitlines()[-1] if proc.stderr else 'no output'}",
                  file=sys.stderr)
            continue
        rows.append(parse(vert, repo, proc.stdout))

    with open(args.json, "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")

    lines = ["# Axis panel: all banked cells, from disk (observational; headline stays with the judge)",
             "",
             "| cell | recall b→s | cost Δ | time Δ | calls Δ | nav b→s | reli spread b→s | ground b→s |",
             "|---|---|---|---|---|---|---|---|"]
    for r in rows:
        cr = r.get("cited_recall", {})
        reli = r.get("reliability", {})
        lines.append("| {v}/{r} | {cb}→{cs} | {cost} | {wall} | {calls} | {navb}→{navs} | {rb}→{rs} | {gb}→{gs} |".format(
            v=r["vertical"], r=r["repo"],
            cb=cr.get("baseline", "?"), cs=cr.get("sense", "?"),
            cost=r.get("cost_usd", {}).get("delta", "?"),
            wall=r.get("wall_s", {}).get("delta", "?"),
            calls=r.get("tool_calls", {}).get("delta", "?"),
            navb=r.get("nav", {}).get("baseline", "?"), navs=r.get("nav", {}).get("sense", "?"),
            rb=reli.get("baseline", {}).get("spread", "?"), rs=reli.get("sense", {}).get("spread", "?"),
            gb=r.get("grounding", {}).get("baseline", "?"), gs=r.get("grounding", {}).get("sense", "?")))
    with open(args.md, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"{len(rows)} cells → {args.json}\n{args.md}")


if __name__ == "__main__":
    main()
