#!/usr/bin/env python3
"""rescore_diff.py, the STOPPER instrument: what does a scoring change do to the record?

$0, always. Frozen transcripts + a changed scorer = a re-score, never a re-bench. Answers
the only two questions that matter when a measurement instrument changes:

  1. HOW MANY recorded runs move?          (the blast radius)
  2. Does any VERDICT move?                (the decision impact)

and it answers (2) in EXACT arithmetic. A float mean of per-run recalls reports 1/2 as
0.49999999999999994 and silently turns a WIN into a tie; that happened once and
invented two verdict flips that did not exist (decision-errors.md, Class 6 clause 3).

Usage:
  python3 bench/lib/rescore_diff.py --old <git-ref-or-path> [--vertical go] [--json out.json]

  --old  a git ref (`HEAD`, a SHA) to read the PREVIOUS gold.py from, or a path to a
         copy of it. The CURRENT working-tree gold.py is the new side.

Prints, and this is what a `stopper/<slug>` ledger entry must carry:
  - runs re-scored / runs changed  (the required "N of M" figure)
  - the CONTROL: does the OLD code reproduce the on-disk numbers? If it does not, the
    harness is wrong and NOTHING below it may be believed (Class-6 clause 2).
  - per-cell deltas old vs new, in exact Fractions, with any verdict move flagged.

Design note: this diffs gold.py because gold_recall is the headline. When another
instrument in the chain changes (judge.py, admission_gate.py), extend `score_run`
rather than hand-rolling a one-off script in a scratchpad; that hand-rolling is what
made 2026-07-15's first three answers wrong.
"""

import argparse
import glob
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

WIN_BAR = Fraction(1, 2)          # "help the AI" = reach at held cost, >= +0.50
REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))


_OLD_MTIME = None


def _old_gold_mtime():
    """Unix time of gold.py's last commit: the cutoff below which an on-disk number
    was produced by an EARLIER scorer and a control mismatch is expected, not a bug."""
    global _OLD_MTIME
    if _OLD_MTIME is None:
        r = subprocess.run(["git", "-C", REPO_ROOT, "log", "-1", "--format=%ct", "--", "bench/lib/gold.py"],
                           capture_output=True, text=True)
        _OLD_MTIME = float(r.stdout.strip()) if r.returncode == 0 and r.stdout.strip() else 0.0
    return _OLD_MTIME


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def load_old_gold(ref):
    """The previous gold.py, from a git ref or a path."""
    if os.path.isfile(ref):
        return _load("gold_old", ref)
    blob = subprocess.run(["git", "-C", REPO_ROOT, "show", f"{ref}:bench/lib/gold.py"],
                          capture_output=True, text=True)
    if blob.returncode != 0:
        sys.exit(f"rescore_diff: cannot read gold.py at ref {ref!r}: {blob.stderr.strip()}")
    fh = tempfile.NamedTemporaryFile("w", suffix=".py", delete=False)
    fh.write(blob.stdout)
    fh.close()
    return _load("gold_old", fh.name)


def scenarios_for(vertical):
    import yaml
    out = {}
    for f in glob.glob(f"{REPO_ROOT}/bench/verticals/{vertical}/scenarios/*.yaml"):
        try:
            y = yaml.safe_load(open(f))
        except Exception:
            continue
        if y and y.get("gold"):
            out[y.get("name")] = y["gold"]
    return out


def score_run(mod, answer, gold, group=None):
    """(cited, total) for one run under one gold module, as exact integers."""
    r = mod.score_gold_recall(answer, gold)
    if group:
        rows = [x for x in r["details"] if x["group"] == group]
    else:
        rows = r["details"]
    return sum(1 for x in rows if x["cited"]), len(rows)


def collect(vertical, old, new):
    from scorer import read_transcript_texts
    scen = scenarios_for(vertical)
    runs, cells = [], {}
    for sp in glob.glob(f"{REPO_ROOT}/bench/verticals/{vertical}/results/**/scored.json", recursive=True):
        d = json.load(open(sp))
        gr = d.get("gold_recall")
        if not gr or d.get("scenario") not in scen:
            continue
        tp = os.path.join(os.path.dirname(sp), "transcript.json")
        if not os.path.isfile(tp):
            continue
        try:
            answer, _ = read_transcript_texts(tp)
        except Exception:
            continue
        gold = scen[d["scenario"]]
        oc, ot = score_run(old, answer, gold)
        nc, nt = score_run(new, answer, gold)
        rel = os.path.relpath(sp, REPO_ROOT)
        # CONTROL: the old code must reproduce what is on disk, or the harness is wrong.
        # EXCEPT when the run was scored BEFORE the old code's own last change; then the
        # on-disk number is simply stale (an earlier scorer produced it) and a mismatch is
        # expected, not a bug. Distinguishing the two mechanically is what keeps the control
        # trustworthy instead of noisy: an UNEXPLAINED mismatch invalidates the whole run.
        control_ok = oc == gr.get("cited")
        stale = (not control_ok) and os.path.getmtime(sp) < _old_gold_mtime()
        runs.append({
            "run": rel,
            "disk_cited": gr.get("cited"),
            "old_cited": oc,
            "new_cited": nc,
            "total": ot,
            "changed": oc != nc,
            "control_ok": control_ok,
            "stale_on_disk": stale,
        })
        parts = rel.split("/results/")[1].split("/")
        if len(parts) >= 4 and parts[1] in ("baseline", "sense") and "_invalid" not in rel and ".bak" not in rel:
            cells.setdefault((parts[0], parts[2]), {}).setdefault(parts[1], []).append(
                (Fraction(oc, ot), Fraction(nc, nt)))
    return runs, cells


def verdict(delta):
    return "WIN" if delta >= WIN_BAR else ("LOSS" if delta <= -WIN_BAR else "tie")


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--old", default="HEAD", help="git ref or path holding the PREVIOUS gold.py")
    ap.add_argument("--vertical", default=None, help="one vertical; default = all")
    ap.add_argument("--json", default=None)
    a = ap.parse_args(argv)

    old = load_old_gold(a.old)
    new = _load("gold_new", os.path.join(REPO_ROOT, "bench", "lib", "gold.py"))
    verticals = [a.vertical] if a.vertical else ["ruby-rails", "python-django", "go"]

    all_runs, all_cells = [], {}
    for v in verticals:
        runs, cells = collect(v, old, new)
        all_runs += runs
        for k, arms in cells.items():
            all_cells[(v,) + k] = arms

    changed = [r for r in all_runs if r["changed"]]
    broken = [r for r in all_runs if not r["control_ok"] and not r["stale_on_disk"]]
    stale = [r for r in all_runs if r["stale_on_disk"]]
    print(f"rescore_diff: {len(all_runs)} runs re-scored, {len(changed)} CHANGED "
          f"({100 * len(changed) / max(1, len(all_runs)):.0f}%)")
    if broken:
        print(f"  !! CONTROL FAILED on {len(broken)} run(s): the OLD code does not reproduce the")
        print( "     on-disk number, so this harness is wrong and NOTHING here may be believed.")
        for r in broken[:5]:
            print(f"       {r['run']}: disk={r['disk_cited']} old={r['old_cited']}")
    else:
        ok = len(all_runs) - len(stale)
        print(f"  control OK: the old code reproduces the on-disk number on {ok}/{ok} eligible runs")
    if stale:
        print(f"  note: {len(stale)} run(s) were scored BEFORE gold.py's last change; their on-disk")
        print( "        numbers are stale by construction (an earlier scorer wrote them), not wrong:")
        for r in stale[:5]:
            print(f"          {r['run']}: disk={r['disk_cited']} old={r['old_cited']}")

    moves = []
    for k in sorted(all_cells):
        arms = all_cells[k]
        if "baseline" not in arms or "sense" not in arms:
            continue
        def mean(xs, i):
            return sum(x[i] for x in xs) / len(xs)
        od = mean(arms["sense"], 0) - mean(arms["baseline"], 0)
        nd = mean(arms["sense"], 1) - mean(arms["baseline"], 1)
        ov, nv = verdict(od), verdict(nd)
        if ov != nv:
            moves.append((k, od, nd, ov, nv))
        if od != nd:
            flag = "   <<< VERDICT MOVES" if ov != nv else ""
            print(f"  {'/'.join(k):50s} Δ {float(od):+.4f} -> {float(nd):+.4f}  ({ov}->{nv}){flag}")
    print(f"\nverdict moves: {len(moves)}" + ("" if moves else ": every verdict survives"))
    if a.json:
        json.dump({"runs": len(all_runs), "changed": len(changed),
                   "control_failures": len(broken),
                   "verdict_moves": [{"cell": "/".join(k), "old": str(o), "new": str(n)}
                                     for k, o, n, _, _ in moves]},
                  open(a.json, "w"), indent=1)
    # Exit 1 when the record moves: a stopper is a stop, not a notification.
    return 1 if (changed or broken) else 0


if __name__ == "__main__":
    sys.exit(main())
