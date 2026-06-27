#!/usr/bin/env python3
"""Aggregate one repo's --runs N result into a variance row (per model).

Reads bench/results/<arm>/<repo>/run-*/ (the N runs just produced for one
model) and prints a markdown block: per arm, the gold_cited and local score
for each run, plus the mean. The spread is the point — it shows whether a
headline number is stable or noise.

Usage: variance-row.py <repo> <model-label>
"""

import glob
import json
import os
import statistics as st
import sys

repo, model = sys.argv[1], sys.argv[2]
# RESULTS_DIR (exported by bench-paths.sh) points at the active bench's root;
# for a vertical that is bench/verticals/<name>/results. Falls back to the global root.
RES = os.environ.get("RESULTS_DIR") or "bench/results"


def run_dirs(arm):
    ds = sorted(glob.glob(f"{RES}/{arm}/{repo}/run-*"))
    return ds or [f"{RES}/{arm}/{repo}"]


def one(d):
    s = json.load(open(f"{d}/scored.json"))
    j = json.load(open(f"{d}/judged.json"))
    cited = s["gold_recall"]["cited_recall"]
    mention = s["gold_recall"]["mention_recall"]
    steps = [x["step_quality"] for x in j.get("steps", []) if x.get("step_quality") is not None]
    q = st.mean(steps) if steps else 0.0
    eff = s.get("efficiency", 0.0)
    local = 0.45 * cited + 0.35 * q + 0.20 * eff
    return cited, mention, local


print(f"\n## {model}\n")
print("| arm | gold_mention per run | mean | gold_cited per run | mean | local per run | mean |")
print("|---|---|---|---|---|---|---|")
for arm in ("baseline", "sense"):
    cs, ms, ls = [], [], []
    for d in run_dirs(arm):
        try:
            c, m, l = one(d)
            cs.append(c)
            ms.append(m)
            ls.append(l)
        except Exception:
            pass
    if not cs:
        print(f"| {arm} | (no data) | — | — | — | — | — |")
        continue
    mstr = ", ".join(f"{m:.0%}" for m in ms)
    cstr = ", ".join(f"{c:.0%}" for c in cs)
    lstr = ", ".join(f"{l:.2f}" for l in ls)
    print(f"| {arm} | {mstr} | **{st.mean(ms):.0%}** | {cstr} | **{st.mean(cs):.0%}** | {lstr} | **{st.mean(ls):.2f}** |")
