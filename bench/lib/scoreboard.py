#!/usr/bin/env python3
"""Regenerate the canonical Rails-vertical scoreboard FROM DISK (reproducible).

Reads the run-layout (results/<model>/<arm>/<repo>/run-*/{scored,judged}.json),
averages each axis across runs, and emits the markdown board table. This is the
durable replacement for the ephemeral /tmp script that produced an
un-reproducible SENSE-SCORING-REPORT.md (the numbers did not trace to disk).

  cited_recall      from scored.json gold_recall.cited_recall  (OBJECTIVE)
  deps-delta        scored.json gold_recall.groups.dependents (cited/total)
  sense-only        dependents cited by sense, never by baseline (any run) — gold_recall.details
  related_recall    judged.json relationship_audit.related_recall (Sonnet)
  grounded_prec     1 - sum(contradicted)/sum(covered) across runs
  contradictions    sum(contradicted) across runs, per arm
  B-score           0.55*cited + 0.25*related + 0.20*grounded_precision
  eff-at-parity     ◆ on a recall TIE only: every sense run under baseline on
                    billed tokens (no overlap) AND lower wall median. The
                    declared headline for enumerable-gold repos (llm.rb) where
                    recall ties BY DESIGN. Reported separately, NOT in the win
                    count, NOT in B-score (efficiency stays out of the blend).

Usage: scoreboard.py [results_dir]   (default: claude-opus-4-8 vertical root)
"""
import json
import glob
import os
import sys

REPO_ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", ".."))
DEFAULT = os.path.join(REPO_ROOT, "bench", "verticals", "ruby-rails",
                       "results", "claude-opus-4-8")
ORDER = ["mastodon", "gitlabhq", "chatwoot", "discourse", "solidus", "forem",
         "ruby_llm", "redmine", "rails", "llm.rb", "langchainrb", "lobsters", "raix"]
GEMS = {"ruby_llm", "llm.rb", "langchainrb", "raix"}


def _mean(a):
    return sum(a) / len(a) if a else None


def _runs(arm_repo):
    fs = sorted(glob.glob(os.path.join(arm_repo, "run-*", "scored.json")))
    flat = os.path.join(arm_repo, "scored.json")
    return fs or ([flat] if os.path.exists(flat) else [])


def arm_axes(root, arm, repo):
    cited, deps, rel = [], [], []
    cov_t = con_t = 0
    judge_model = None
    dep_items = set()   # union of dependents-group gold ids this arm CITED (any run)
    counts = []         # overall cited-gold count per run (for reliability/floor)
    toks, walls = [], []  # per-run billed tokens / wall seconds (efficiency-at-parity)
    for sf in _runs(os.path.join(root, arm, repo)):
        s = json.load(open(sf))
        g = s.get("gold_recall", {})
        cited.append(g.get("cited_recall", 0.0))
        me = s.get("metrics", {}) or {}
        if me.get("token_total_billed") is not None:
            toks.append(me["token_total_billed"])
        if me.get("wall_time_seconds") is not None:
            walls.append(me["wall_time_seconds"])
        grp = g.get("groups", {}).get("dependents")
        if grp and grp.get("total"):
            deps.append(grp["cited"] / grp["total"])
        det = g.get("details", []) or []
        counts.append(sum(1 for x in det if x.get("cited")))
        dep_items |= {x["id"] for x in det
                      if x.get("cited") and x.get("group") == "dependents"}
        jf = sf.replace("scored.json", "judged.json")
        if os.path.exists(jf):
            j = json.load(open(jf))
            judge_model = j.get("judge", {}).get("model", judge_model)
            ra = j.get("relationship_audit", {}) or {}
            if ra.get("related_recall") is not None:
                rel.append(ra["related_recall"])
            cov_t += ra.get("covered", 0) or 0
            con_t += ra.get("contradicted", 0) or 0
    gp = (1 - con_t / cov_t) if cov_t else 1.0
    return {"cited": _mean(cited), "deps": _mean(deps), "related": _mean(rel),
            "grounded": gp, "contra": con_t, "n": len(cited), "judge": judge_model,
            "dep_items": dep_items, "counts": counts, "toks": toks, "walls": walls}


def bscore(a):
    return 0.55 * (a["cited"] or 0) + 0.25 * (a["related"] or 0) + 0.20 * a["grounded"]


def eff_at_parity(b, s):
    """Robust within-agent efficiency win at HELD recall.

    Only meaningful when the cited-recall verdict is a TIE (the caller gates on
    that): a scenario whose declared headline is efficiency (llm.rb) ties on
    recall BY DESIGN, so the real result lives here. Robust = every sense run's
    billed tokens strictly below every baseline run's (no run overlap) AND the
    sense wall-time median below the baseline's. Returns the token Δ% (negative =
    sense cheaper) when both hold, else None. NOT folded into the cited headline
    or B-score; reported as a separate, clearly-labelled axis (efficiency was
    deliberately excluded from the recall blend, this keeps it that way).
    """
    bt, st, bw, sw = b["toks"], s["toks"], b["walls"], s["walls"]
    if not (bt and st and bw and sw):
        return None
    tokens_robust = max(st) < min(bt)          # no overlap, sense strictly under
    wall_lower = _median(sw) < _median(bw)
    if tokens_robust and wall_lower:
        return (_mean(st) - _mean(bt)) / _mean(bt) * 100.0
    return None


def _median(a):
    a = sorted(a)
    n = len(a)
    return a[n // 2] if n % 2 else (a[n // 2 - 1] + a[n // 2]) / 2


def build(root):
    rows = []
    wins = ties = losses = eff_wins = 0
    deltas = []
    judges = set()
    for repo in ORDER:
        b = arm_axes(root, "baseline", repo)
        s = arm_axes(root, "sense", repo)
        if b["cited"] is None or s["cited"] is None:
            continue
        d = s["cited"] - b["cited"]
        deltas.append(d)
        verdict = "WIN" if d > 0.005 else ("TIE" if abs(d) <= 0.005 else "LOSS")
        wins += verdict == "WIN"
        ties += verdict == "TIE"
        losses += verdict == "LOSS"
        antifab = b["contra"] > 0 and s["contra"] == 0
        # sense-only reach: dependents the sense arm cited that the baseline
        # never cited in ANY run (the silent breaks a grep-driven refactor ships).
        s["sense_only"] = len(s["dep_items"] - b["dep_items"])
        # efficiency-at-parity: only surfaced on a recall TIE, where a scenario
        # whose declared headline is efficiency (llm.rb) actually lands its win.
        # Kept separate from the cited verdict, never inflates the win count.
        eff = eff_at_parity(b, s) if verdict == "TIE" else None
        if eff is not None:
            eff_wins += 1
        judges.add(b["judge"]); judges.add(s["judge"])
        rows.append((repo, b, s, d, verdict, antifab, eff))
    return rows, wins, ties, losses, eff_wins, _mean(deltas), judges


def markdown(root):
    rows, wins, ties, losses, eff_wins, mean_d, judges = build(root)
    judge = sorted(j for j in judges if j) or ["?"]
    out = []
    out.append("# Rails-vertical scoring — Sense vs baseline\n")
    out.append(f"_Judge: {', '.join(judge)} (via subscription CLI). All axes "
               "averaged across runs. Regenerated FROM DISK by "
               "`bench/lib/scoreboard.py` (reproducible).\n")
    out.append("## Headline")
    out.append(f"**{wins} wins / {ties} ties / {losses} losses** across "
               f"{len(rows)} repos on `cited_recall`. "
               f"Mean cited Δ (sense−baseline): **{mean_d:+.3f}**.\n")
    af = [r[0] for r in rows if r[5]]
    out.append(f"Anti-fabrication wins (⚑ — baseline asserts a wrong relation, "
               f"Sense does not): **{', '.join(af) if af else 'none'}**.\n")
    ew = [(r[0], r[6]) for r in rows if r[6] is not None]
    if ew:
        ewtxt = ", ".join(f"{repo} ({eff:+.0f}% tokens)" for repo, eff in ew)
        out.append(f"Efficiency-at-parity wins (◆, recall held, every sense run robustly "
                   f"under baseline on billed tokens AND wall time): **{ewtxt}**. "
                   f"Counted separately, never folded into the cited-recall headline.\n")
    out.append("| repo | cited b→s (Δ) | deps-delta | sense-only | B-score b→s | related b→s | grnd b/s | contra b/s | verdict |")
    out.append("|------|---------------|-----------|:---------:|-------------|-------------|:--------:|:---------:|:-------:|")
    for repo, b, s, d, verdict, antifab, eff in rows:
        gem = " 🔸" if repo in GEMS else ""
        flag = " ⚑" if antifab else ""
        effflag = f" ◆ eff {eff:+.0f}% tok" if eff is not None else ""
        dd = (f"{s['deps']-b['deps']:+.2f}" if (b['deps'] is not None and s['deps'] is not None) else "—")
        rb = f"{b['related']:.2f}" if b['related'] is not None else "—"
        rs = f"{s['related']:.2f}" if s['related'] is not None else "—"
        out.append(f"| {repo}{gem} | {b['cited']:.2f}→{s['cited']:.2f} ({d:+.2f}) "
                   f"| {dd} | {s['sense_only']} | {bscore(b):.2f}→{bscore(s):.2f} | {rb}→{rs} "
                   f"| {b['grounded']:.2f}/{s['grounded']:.2f} "
                   f"| {b['contra']}/{s['contra']} | **{verdict}{flag}{effflag}** |")
    out.append("\n🔸 gem / small library. ⚑ anti-fabrication win. "
               "◆ efficiency-at-parity win (recall tied, sense robustly cheaper in billed tokens + wall time, "
               "the declared headline axis for an enumerable-gold repo like llm.rb; counted separately from cited wins). "
               "deps-delta = cited_recall on the scattered-dependents discriminator group. "
               "sense-only = dependents the sense arm cited that the baseline never cited in any run "
               "(the silent breaks a grep-driven refactor would ship).")
    return "\n".join(out) + "\n"


if __name__ == "__main__":
    root = sys.argv[1] if len(sys.argv) > 1 else DEFAULT
    sys.stdout.write(markdown(root))
