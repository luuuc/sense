#!/usr/bin/env python3
"""Dry-run: re-score gold_recall with the path-compaction oracle ON, diff vs the
literal matcher. No scored.json is written — read-only. Prints every changed
(repo, arm, run, target) with the exact compacted answer-string and the full
real path it grounds to, so each new credit is individually auditable.

Usage: python3 lib/dryrun_path_oracle.py [model_dir] [repo_glob]
  model_dir default: claude-opus-4-8
"""
import glob
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(__file__))
import yaml  # noqa: E402

import gold as G  # noqa: E402
from judge import read_answer_text  # noqa: E402

SENSE_REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))  # oss/sense
RESULTS = os.path.join(SENSE_REPO, "bench", "verticals", "ruby-rails", "results")
SCEN = os.path.join(SENSE_REPO, "bench", "verticals", "ruby-rails", "scenarios")
INDEX = os.path.join(os.path.dirname(SENSE_REPO), "sense-benchmark", "sense")  # per-repo .sense


def load_gold_by_repo():
    out = {}
    for f in glob.glob(os.path.join(SCEN, "*.yaml")):
        if ".rubric" in f:
            continue
        try:
            d = yaml.safe_load(open(f))
        except Exception:
            continue
        if d and d.get("repo") and d.get("gold"):
            out[d["repo"]] = d["gold"]
    return out


def repo_files(repo):
    db = os.path.join(INDEX, repo, ".sense", "index.db")
    if not os.path.isfile(db):
        return None
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        return [r[0] for r in con.execute("SELECT path FROM sense_files")]
    finally:
        con.close()


def main():
    model = sys.argv[1] if len(sys.argv) > 1 else "claude-opus-4-8"
    repo_glob = sys.argv[2] if len(sys.argv) > 2 else "*"
    gold_by_repo = load_gold_by_repo()
    files_cache = {}

    totals = {"runs": 0, "men_old": 0, "men_new": 0, "cit_old": 0, "cit_new": 0,
              "targets": 0, "newly_mentioned": 0, "newly_cited": 0}
    per_repo = {}
    # per-arm cited_recall / mention_recall means (the headline is cited_recall)
    arm_cr = {"baseline": {"old": [], "new": []}, "sense": {"old": [], "new": []}}

    for arm in ("baseline", "sense"):
        for repo_dir in sorted(glob.glob(os.path.join(RESULTS, model, arm, repo_glob))):
            repo = os.path.basename(repo_dir)
            gold = gold_by_repo.get(repo)
            if not gold:
                continue
            if repo not in files_cache:
                files_cache[repo] = repo_files(repo)
            rf = files_cache[repo]
            for run_dir in sorted(glob.glob(os.path.join(repo_dir, "run-*"))):
                tpath = os.path.join(run_dir, "transcript.json")
                if not os.path.isfile(tpath):
                    continue
                answer = read_answer_text(tpath)
                tx = open(tpath, encoding="utf-8", errors="ignore").read()
                old = G.score_gold_recall(answer, gold)
                new = G.score_gold_recall(answer, gold, repo_files=rf, transcript_text=tx)
                arm_cr[arm]["old"].append(old["cited_recall"])
                arm_cr[arm]["new"].append(new["cited_recall"])
                totals["runs"] += 1
                totals["targets"] += old["total"]
                totals["men_old"] += old["mentioned"]
                totals["men_new"] += new["mentioned"]
                totals["cit_old"] += old["cited"]
                totals["cit_new"] += new["cited"]
                pr = per_repo.setdefault(repo, {"men_d": 0, "cit_d": 0, "n": 0,
                                                "men_old": 0, "men_new": 0})
                pr["n"] += 1
                pr["men_old"] += old["mentioned"]
                pr["men_new"] += new["mentioned"]
                pr["men_d"] += new["mentioned"] - old["mentioned"]
                pr["cit_d"] += new["cited"] - old["cited"]

                om = {d["id"]: d for d in old["details"]}
                changed = []
                for d in new["details"]:
                    o = om[d["id"]]
                    if d["mentioned"] and not o["mentioned"]:
                        totals["newly_mentioned"] += 1
                        changed.append(("MENTION", d["id"], gold))
                    if d["cited"] and not o["cited"]:
                        totals["newly_cited"] += 1
                        changed.append(("CITE", d["id"], gold))
                if changed:
                    tag = f"{arm}/{repo}/{os.path.basename(run_dir)}"
                    print(f"\n  {tag}: men {old['mentioned']}->{new['mentioned']}  "
                          f"cit {old['cited']}->{new['cited']}")
                    hay = answer.lower()
                    for kind, tid, gd in changed:
                        # recover evidence
                        item = next((x for x in gd if (isinstance(x, dict) and x.get("id") == tid)), None)
                        pat = (item.get("match") or [tid])[0].lower() if item else tid
                        real = G._resolve_real(pat, rf) if rf else None
                        suf = G._min_unique_suffix(real, rf) if real else None
                        present = suf and suf in hay
                        print(f"      +{kind:7} {tid:16} gold='{pat}'  real='{real}'  "
                              f"suffix='{suf}'  in_answer={bool(present)}")

    print("\n" + "=" * 60)
    print(f"{model}  runs={totals['runs']}  gold-targets={totals['targets']}")
    print(f"  mention total: {totals['men_old']} -> {totals['men_new']}  "
          f"(+{totals['men_new']-totals['men_old']})")
    print(f"  cited   total: {totals['cit_old']} -> {totals['cit_new']}  "
          f"(+{totals['cit_new']-totals['cit_old']})")
    print(f"  newly mentioned targets: {totals['newly_mentioned']}   "
          f"newly cited: {totals['newly_cited']}")
    print("\n  per-repo (mention delta summed over runs/arms):")
    for repo, pr in sorted(per_repo.items()):
        if pr["men_d"] or pr["cit_d"]:
            print(f"    {repo:14} runs={pr['n']:2}  men +{pr['men_d']}  cit +{pr['cit_d']}")
    def mean(xs):
        return sum(xs) / len(xs) if xs else 0.0
    print("\n  cited_recall mean (headline) per arm  [old -> new]:")
    for a in ("baseline", "sense"):
        o, n = mean(arm_cr[a]["old"]), mean(arm_cr[a]["new"])
        print(f"    {a:9} {o:.3f} -> {n:.3f}   (n={len(arm_cr[a]['old'])})")
    bo, bn = mean(arm_cr["baseline"]["old"]), mean(arm_cr["baseline"]["new"])
    so, sn = mean(arm_cr["sense"]["old"]), mean(arm_cr["sense"]["new"])
    print(f"    sense-baseline gap: {so-bo:+.3f} -> {sn-bn:+.3f}")
    missing = [r for r, v in files_cache.items() if v is None]
    if missing:
        print(f"\n  NOTE: no .sense index for: {missing} (oracle off for these)")


if __name__ == "__main__":
    main()
