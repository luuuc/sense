#!/usr/bin/env python3
"""Per-group objective cited-recall, baseline vs sense, across all runs.

The truth-check the win/no-win loop depends on: separation hides in the SCATTERED
gold group (the indirect-edge dependents), diluted by grep-easy padding (contract /
direct / colocated groups both arms always get). This prints cited-recall per group
per run for each arm, the means, and flags any group where sense beats baseline by
>= the threshold (default 0.50 — the campaign bar).

  python3 bench/lib/pergroup.py <repo> [threshold]

Reads bench/results/{baseline,sense}/<repo>/run-*/scored.json (falls back to the
flat scored.json for single runs). Objective only — no judge, no LLM. This is the
HEADLINE metric; the reference-aware audit (rescore_audit.py) is secondary and
over-credits homogeneous fan-outs.
"""
import json, os, sys, glob

REPO = sys.argv[1] if len(sys.argv) > 1 else sys.exit("usage: pergroup.py <repo> [threshold]")
THRESH = float(sys.argv[2]) if len(sys.argv) > 2 else 0.50
ROOT = os.path.join(os.path.dirname(__file__), "..", "results")


def runs(arm):
    base = os.path.join(ROOT, arm, REPO)
    paths = sorted(glob.glob(os.path.join(base, "run-*", "scored.json")))
    if not paths and os.path.exists(os.path.join(base, "scored.json")):
        paths = [os.path.join(base, "scored.json")]
    return paths


def collect(arm):
    by_group, overall = {}, []
    for p in runs(arm):
        d = json.load(open(p))
        g = d.get("gold_recall", {})
        overall.append(g.get("cited_recall", 0.0))
        for gn, gd in g.get("groups", {}).items():
            by_group.setdefault(gn, []).append((gd.get("cited", 0), gd.get("total", 0)))
    return by_group, overall


bg, bo = collect("baseline")
sg, so = collect("sense")
if not bo or not so:
    sys.exit(f"no scored runs for {REPO} (baseline={len(bo)} sense={len(so)}) — bench it first")

print(f"### {REPO} — per-group objective cited-recall (threshold +{THRESH:.0%})\n")
print(f"{'group':16} {'baseline (per run)':28} {'sense (per run)':28}  delta")
groups = sorted(set(bg) | set(sg))
for gn in groups:
    b = bg.get(gn, []); s = sg.get(gn, [])
    bm = sum(c for c, _ in b) / sum(t for _, t in b) if any(t for _, t in b) else 0.0
    sm = sum(c for c, _ in s) / sum(t for _, t in s) if any(t for _, t in s) else 0.0
    bstr = ", ".join(f"{c}/{t}" for c, t in b)
    sstr = ", ".join(f"{c}/{t}" for c, t in s)
    flag = "  <== WIN >= threshold" if (sm - bm) >= THRESH else ("  <- sense ahead" if sm - bm > 0.05 else "")
    print(f"{gn:16} {bstr:28} {sstr:28}  {sm-bm:+.2f}{flag}")

bmean = sum(bo) / len(bo); smean = sum(so) / len(so)
print(f"\noverall cited-recall  baseline {[round(x,2) for x in bo]} mean {bmean:.2f}"
      f"  |  sense {[round(x,2) for x in so]} mean {smean:.2f}  delta {smean-bmean:+.2f}")
print(f"\nVERDICT: {'WIN' if any((sum(c for c,_ in sg.get(gn,[]))/max(1,sum(t for _,t in sg.get(gn,[]))) - sum(c for c,_ in bg.get(gn,[]))/max(1,sum(t for _,t in bg.get(gn,[])))) >= THRESH for gn in groups) else 'NOT YET >=threshold — re-author toward the indirect-edge seam or prove colocated/resolver-gap'}")
