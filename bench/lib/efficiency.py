#!/usr/bin/env python3
"""Per-repo WITHIN-AGENT efficiency / reach / reliability numbers, FROM DISK.

Feeds the Block D efficiency & effectiveness sub-table + the reach & reliability
line (see _skeleton.md). Everything here is a within-agent comparison
(baseline vs sense inside ONE agent); never compare across agents.

  billed tokens   mean(metrics.token_total_billed)
  session time    median(metrics.wall_time_seconds)   (noisy; median across runs)
  cost            mean(metrics.cost_usd)
  turnarounds     mean(metrics.tool_calls)
  navigation      mean(grep_count / mcp_count / read_count)
  grounding       1 - sum(contradicted)/sum(covered)  (judged.json, anti-fab)
  sense-only      dependents cited by sense, never by baseline (gold_recall.details)
  reliability     per-run overall cited-gold counts per arm (the floor)
  miss-list       baseline gold_recall.missed_cite never reached in any run

Usage: efficiency.py <repo> [results_dir]
       results_dir defaults to the claude-opus-4-8 vertical root.
"""
import json
import glob
import os
import statistics
import sys

REPO_ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", ".."))
DEFAULT = os.path.join(REPO_ROOT, "bench", "verticals", "ruby-rails",
                       "results", "claude-opus-4-8")


def _runs(arm_repo):
    fs = sorted(glob.glob(os.path.join(arm_repo, "run-*", "scored.json")))
    flat = os.path.join(arm_repo, "scored.json")
    return fs or ([flat] if os.path.exists(flat) else [])


def _mean(a):
    return sum(a) / len(a) if a else None


def arm(root, which, repo):
    m = {k: [] for k in ("tok", "wall", "cost", "calls", "grep", "mcp", "read")}
    cited_counts = []          # overall cited-gold count per run (reliability)
    dep_items = set()          # dependents cited (any run)
    cited_recalls = []
    miss_sets = []             # missed_cite per run (for baseline never-cited)
    cov_t = con_t = 0
    for sf in _runs(os.path.join(root, which, repo)):
        s = json.load(open(sf))
        me = s.get("metrics", {}) or {}
        m["tok"].append(me.get("token_total_billed"))
        m["wall"].append(me.get("wall_time_seconds"))
        m["cost"].append(me.get("cost_usd"))
        m["calls"].append(me.get("tool_calls"))
        m["grep"].append(me.get("grep_count"))
        m["mcp"].append(me.get("mcp_count"))
        m["read"].append(me.get("read_count"))
        g = s.get("gold_recall", {}) or {}
        cited_recalls.append(g.get("cited_recall"))
        det = g.get("details", []) or []
        cited_counts.append(sum(1 for x in det if x.get("cited")))
        dep_items |= {x["id"] for x in det
                      if x.get("cited") and x.get("group") == "dependents"}
        miss_sets.append(set(g.get("missed_cite", []) or []))
        jf = sf.replace("scored.json", "judged.json")
        if os.path.exists(jf):
            ra = (json.load(open(jf)).get("relationship_audit", {}) or {})
            cov_t += ra.get("covered", 0) or 0
            con_t += ra.get("contradicted", 0) or 0
    clean = {k: [x for x in v if x is not None] for k, v in m.items()}
    grounded = (1 - con_t / cov_t) if cov_t else 1.0
    return {
        "tok": _mean(clean["tok"]),
        "wall": statistics.median(clean["wall"]) if clean["wall"] else None,
        "cost": _mean(clean["cost"]),
        "calls": _mean(clean["calls"]),
        "grep": _mean(clean["grep"]), "mcp": _mean(clean["mcp"]),
        "read": _mean(clean["read"]),
        "grounded": grounded, "contra": con_t,
        "cited_recall": _mean([c for c in cited_recalls if c is not None]),
        "cited_counts": cited_counts,
        "dep_items": dep_items,
        "miss_sets": miss_sets,
    }


def _pct(b, s):
    if not b or s is None:
        return "—"
    return f"{(s - b) / b * 100:+.0f}%"


def report(root, repo):
    b = arm(root, "baseline", repo)
    s = arm(root, "sense", repo)
    sense_only = sorted(s["dep_items"] - b["dep_items"])
    # baseline gold it NEVER cited in any run = intersection of per-run miss sets
    never = set.intersection(*b["miss_sets"]) if b["miss_sets"] else set()
    mult = (s["cited_recall"] / b["cited_recall"]
            if b["cited_recall"] else None)
    L = []
    L.append(f"# {repo} — within-agent efficiency / reach / reliability "
             f"(Claude Code · Opus 4.8, FROM DISK)\n")
    L.append("| axis | baseline | sense | Δ |")
    L.append("|---|---|---|---|")
    L.append(f"| billed tokens | {b['tok']:.0f} | {s['tok']:.0f} | {_pct(b['tok'], s['tok'])} |")
    L.append(f"| session time (s, median) | {b['wall']:.0f} | {s['wall']:.0f} | {_pct(b['wall'], s['wall'])} |")
    L.append(f"| cost ($) | {b['cost']:.2f} | {s['cost']:.2f} | {_pct(b['cost'], s['cost'])} |")
    bc, sc = round(b['calls']), round(s['calls'])
    L.append(f"| turnarounds (tool calls) | {bc} | {sc} | {sc - bc:+d} |")
    L.append(f"| navigation (grep / mcp / read) | {b['grep']:.0f}/{b['mcp']:.0f}/{b['read']:.0f} "
             f"| {s['grep']:.0f}/{s['mcp']:.0f}/{s['read']:.0f} | grep→structural |")
    L.append(f"| grounding (anti-fab) | {b['grounded']:.2f} | {s['grounded']:.2f} "
             f"| contra {b['contra']}→{s['contra']} |")
    L.append("")
    L.append(f"- **sense-only reach (dependents):** {len(sense_only)} "
             f"({', '.join(sense_only) if sense_only else 'none'})")
    L.append(f"- **reach at parity:** cited_recall {b['cited_recall']:.2f}→{s['cited_recall']:.2f}"
             + (f" ({mult:.1f}×)" if mult else "") + f" at {_pct(b['tok'], s['tok'])} tokens")
    L.append(f"- **reliability (overall cited/run):** baseline {b['cited_counts']} "
             f"→ sense {s['cited_counts']}")
    L.append(f"- **baseline never-cited miss-list:** "
             f"{', '.join(sorted(never)) if never else 'none'}")
    return "\n".join(L) + "\n"


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit("usage: efficiency.py <repo> [results_dir]")
    root = sys.argv[2] if len(sys.argv) > 2 else DEFAULT
    sys.stdout.write(report(root, sys.argv[1]))
