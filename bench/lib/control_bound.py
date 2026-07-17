#!/usr/bin/env python3
"""The arithmetic bound: kill a cell the bar makes impossible, before it is paid for.

    control_bound.py <scenario.yaml> <probe_answer.md> [<probe_answer2.md> ...]

Exit 0 = the cell can still clear the bar (spend allowed).
Exit 1 = KILL: no group can reach the bar. The cell is arithmetically dead. Do not spend.

## THE BOUND

    delta = mean(sense) - mean(control),  and  cited_recall <= 1.0
      =>   delta >= BAR   REQUIRES   mean(control) <= 1.0 - BAR

With the campaign bar at +0.50 (pergroup.py's default), a group whose CONTROL scores above
0.50 cannot produce a +0.50 delta even if sense scores a perfect 1.00. This is arithmetic,
not a heuristic, not a threshold fitted to history: it holds for every repo, language,
framework and question shape, because no term in it mentions any of those.

## WHY THIS EXISTS (measured over all 384 frozen scored runs)

Ten of twenty paid cells on the bench model were arithmetically dead BEFORE they ran:
healthchecks 1.00, litellm 1.00 (ceiling EXACTLY 0.000), sentry 0.90, wagtail 0.81,
redmine 0.77, lobsters 0.77, ruby_llm 0.76, netbox 0.67, dolt 0.67, raix 0.60. Each was
killable by one comparison. We paid for all of them. The whole dolt campaign (five cells,
two Loop 7 attempts) ran against a control at 0.67 whose ceiling was +0.33.

## WHY 0.50 AND NOT TIGHTER (the false-negative check; do NOT "improve" this)

Measured against the 5 real wins in the corpus:

    control > 0.50 -> kill   10 cells killed,  0 WINS killed   <- this rule
    control > 0.40 -> kill   13 cells killed,  1 WIN  killed   (saleor)
    control > 0.25 -> kill   14 cells killed,  1 WIN  killed   (saleor)
    control > 0.20 -> kill   17 cells killed,  2 WINS killed   (saleor, discourse)

saleor proves the bound is TIGHT, not merely safe: control 0.50, sense 1.00, delta EXACTLY
+0.500 = the bar. A control of 0.50 is winnable; anything above it is not. Every threshold
below 0.50 is an empirical guess that costs real wins. This one costs none, provably.

## GOLD FIDELITY: "ROUGH GOLD IS SAFE" IS FALSE. DO NOT SCREEN WITH UNAUDITED GOLD.

Measured and REFUTED on gitea 2026-07-16, and it is the trap this gate is most exposed to.

The reasoning was: over-inclusive gold LOWERS the control's recall, biasing toward PASS, and a
false pass costs ~$4 while a false kill costs a win - so rough gold errs cheap. The arithmetic is
right and the conclusion is wrong. **Bias-toward-PASS is not "safe", it is TOOTHLESS**: a gold that
does not answer the ask always passes, so the screen cannot kill anything and tells you nothing.
The real cost of a false pass is not $4, it is authoring a whole cell on a false premise.

The gitea run: gold built from `sense blast Repository --min-confidence 0.3` (42 files), ask =
"the deletion-cascade rework: what must you touch". Control scored 0.238/0.167 -> mean 0.202 ->
PASS, ceiling +0.798. It looked like a floor. It was a GOLD MISMATCH:

  - 32 of 42 gold rows were MISSED, and the misses are routers that render commits, branches,
    feeds and org homes: `routers/web/repo/commit.go` (0 mentions of "delete"),
    `routers/web/feed/repo.go` (0), `routers/common/compare.go` (0). They sit in Repository's
    BLAST RADIUS (affected if the TYPE changes) and are irrelevant to a TEARDOWN rework. The gold
    answered a different question than the ask.
  - Worse, it punished CORRECT answers: the gold demands `models/perm/access/repo_permission.go`
    for the Access row, while both probes cited `services/repository/delete.go:148`
    (`&access_model.Access{RepoID: repo.ID}`) - the same fact, at the file where the cascade
    actually happens.

So: **blast-radius gold != edit-impact gold**, and the hand-audit is NOT skippable. The bound's
arithmetic is only as meaningful as the gold it consumes. Before trusting ANY verdict from this
gate, run the per-unit check: list the rows the control missed and ask whether the ask ever pointed
at them. A surprising PASS is a gold bug until proven otherwise.

## PER-GROUP, AND WHY

pergroup.py (the WIN arbiter) flags a win on ANY gold group, not just `dependents` -
saleor's banked win is on its `context` group. So the bound is evaluated per group and the
cell is killed ONLY when EVERY group is dead. Applying it to one group would re-create the
exact false negative the 0.50 threshold was chosen to avoid.

## SAMPLING (the RUNS=2 law applies here too)

The control's score is a random variable, and it is not a small one: dolt's control swung
8/18 -> 16/18 (0.444 -> 0.889) across two UNCONSTRAINED runs of the same arm and prompt.
The verdict uses the MEAN (because pergroup's delta does), so the mean must be estimated
from >= 2 probe runs. A single probe carries an OPEN flag: the standing RUNS=2 law, applied
to the gate that decides the spend.

The probe MUST be run at the cell's REAL wall. This is not pedantry: dolt's control scores
~0.00 at a 300s wall and 0.67 at 720s - same repo, same gold, same question. The wall IS the
control's score, so a probe at the wrong wall measures a different cell.

Scoring uses gold.score_gold_recall - the SAME instrument the bench scores with. A gate that
scored differently from the arbiter would be a second scorer, and two scorers is a bug
factory (see stopper/gold-basename-false-credit).
"""

import statistics
import sys

import yaml

from gold import score_gold_recall

BAR = 0.50            # pergroup.py's default campaign bar
MAX_RECALL = 1.0      # cited_recall is a ratio
BOUND = MAX_RECALL - BAR   # = 0.50; the highest control that leaves the bar reachable


def score_probe(answer_text, gold):
    """Per-group cited_recall for one control probe answer."""
    gr = score_gold_recall(answer_text, gold)
    return {g: d.get("cited_recall") for g, d in (gr.get("groups") or {}).items()}


def evaluate(scenario_path, probe_paths):
    with open(scenario_path) as fh:
        gold = (yaml.safe_load(fh) or {}).get("gold") or []
    if not gold:
        raise SystemExit(f"control_bound: no gold in {scenario_path}")

    per_run = []
    for p in probe_paths:
        with open(p, errors="ignore") as fh:
            per_run.append(score_probe(fh.read(), gold))

    groups = sorted({g for r in per_run for g in r})
    means = {}
    for g in groups:
        vals = [r[g] for r in per_run if r.get(g) is not None]
        if vals:
            means[g] = statistics.mean(vals)
    return per_run, means


def main(argv):
    if len(argv) < 3:
        raise SystemExit(__doc__.strip().splitlines()[2].strip())
    scenario, probes = argv[1], argv[2:]
    per_run, means = evaluate(scenario, probes)

    n = len(probes)
    print(f"## control bound - bar +{BAR:.2f} requires control <= {BOUND:.2f}")
    print(f"   probe runs: {n}" + ("   [OPEN: n=1, the RUNS=2 law is unmet; "
                                   "the mean is an estimate of one sample]" if n < 2 else ""))
    print()
    print(f"   {'group':<16} {'per-run control':<28} {'mean':>6}   {'ceiling':>8}   verdict")
    alive = []
    for g, m in sorted(means.items()):
        runs = ", ".join(f"{r[g]:.3f}" for r in per_run if r.get(g) is not None)
        ceiling = MAX_RECALL - m
        ok = m <= BOUND
        if ok:
            alive.append(g)
        print(f"   {g:<16} {runs:<28} {m:>6.3f}   {ceiling:>+8.3f}   "
              f"{'reachable' if ok else 'DEAD (ceiling < bar)'}")
    print()

    if alive:
        print(f"   PASS - {len(alive)}/{len(means)} group(s) can still reach +{BAR:.2f}: "
              f"{', '.join(alive)}")
        if n < 2:
            print("   NOTE: n=1. The control's spread is real (dolt: 0.444 -> 0.889 on "
                  "identical prompts). Run a second probe before trusting a near-bound number.")
        return 0

    worst = min(means.values())
    print(f"   KILL - every group's control exceeds {BOUND:.2f}; the best reachable delta is "
          f"{MAX_RECALL - worst:+.3f}, below the +{BAR:.2f} bar.")
    print("   This cell cannot clear the bar even if sense scores a perfect 1.00. Do not spend.")
    print("   Levers: re-target the gold, re-shape the ask, or swap the repo (the swap gate).")
    print("   NOT a lever: the wall. Tightening it lowers the control AND truncates sense "
          "(the dolt campaign measured this: no wall value exists where the audit completes "
          "but both control routes are starved).")
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
