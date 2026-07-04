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
import json, os, sys, glob, statistics

REPO = sys.argv[1] if len(sys.argv) > 1 else sys.exit("usage: pergroup.py <repo> [threshold]")
THRESH = float(sys.argv[2]) if len(sys.argv) > 2 else 0.50

# RESULTS_DIR (exported by bench-paths.sh) pins the active bench's root. When it
# is unset (manual runs), default to the global root, but fall back to whichever
# vertical subtree actually holds this repo so `pergroup.py <vertical-repo>` just
# works without the caller having to know which bench owns it.
_DEFAULT = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", "results"))


def _resolve_root():
    if os.environ.get("RESULTS_DIR"):
        return os.environ["RESULTS_DIR"]
    # Auto-discover which bench root holds this repo. Candidates: the global root,
    # and every vertical model root (verticals/<name>/results/<model>/). A repo may
    # live in several (e.g. discourse is in global + the vertical, and a vertical
    # repo may be benched on multiple models), so when more than one matches, make
    # the caller disambiguate with RESULTS_DIR rather than silently pick one.
    cands = []
    if os.path.isdir(os.path.join(_DEFAULT, "baseline", REPO)):
        cands.append(_DEFAULT)
    _verticals = os.path.join(os.path.dirname(_DEFAULT), "verticals")
    for cand in sorted(glob.glob(os.path.join(_verticals, "*", "results", "*"))):
        if os.path.isdir(os.path.join(cand, "baseline", REPO)):
            cands.append(cand)
    if len(cands) == 1:
        return cands[0]
    if len(cands) > 1:
        rel = "\n  ".join(os.path.relpath(c) for c in cands)
        sys.exit(f"{REPO} is in several bench roots — set RESULTS_DIR to one of:\n  {rel}")
    return _DEFAULT


ROOT = _resolve_root()


def runs(arm):
    base = os.path.join(ROOT, arm, REPO)
    paths = sorted(glob.glob(os.path.join(base, "run-*", "scored.json")))
    if not paths and os.path.exists(os.path.join(base, "scored.json")):
        paths = [os.path.join(base, "scored.json")]
    return paths


def collect(arm):
    by_group, overall, toks, walls = {}, [], [], []
    for p in runs(arm):
        d = json.load(open(p))
        # A failed run (empty_final_answer / truncated stream / provider cap) is
        # NOT a real 0.0 — its own run_meta says so. Blending it as 0.0
        # manufactures a false loss (the Kimi throttle-truncation artifact).
        # Skip it; an arm with no surviving run surfaces as no-data below.
        if d.get("failed"):
            continue
        g = d.get("gold_recall", {})
        overall.append(g.get("cited_recall", 0.0))
        for gn, gd in g.get("groups", {}).items():
            by_group.setdefault(gn, []).append((gd.get("cited", 0), gd.get("total", 0)))
        # Per-run billed tokens + wall drive the efficiency-at-parity axis below
        # (the sentry-shaped hidden win: reach ties, but sense is robustly cheaper).
        m = d.get("metrics", {}) or {}
        if isinstance(m.get("token_total_billed"), (int, float)):
            toks.append(m["token_total_billed"])
        if isinstance(m.get("wall_time_seconds"), (int, float)):
            walls.append(m["wall_time_seconds"])
    return by_group, overall, {"toks": toks, "walls": walls}


bg, bo, beff = collect("baseline")
sg, so, seff = collect("sense")
if not bo or not so:
    sys.exit(f"no scored runs for {REPO} (baseline={len(bo)} sense={len(so)}) — bench it first")

print(f"### {REPO} — per-group objective cited-recall (threshold +{THRESH:.0%})\n")
print(f"{'group':16} {'baseline (per run)':28} {'sense (per run)':28}  delta")
groups = sorted(set(bg) | set(sg))
deltas = {}
for gn in groups:
    b = bg.get(gn, []); s = sg.get(gn, [])
    bm = sum(c for c, _ in b) / sum(t for _, t in b) if any(t for _, t in b) else 0.0
    sm = sum(c for c, _ in s) / sum(t for _, t in s) if any(t for _, t in s) else 0.0
    deltas[gn] = sm - bm
    bstr = ", ".join(f"{c}/{t}" for c, t in b)
    sstr = ", ".join(f"{c}/{t}" for c, t in s)
    flag = "  <== WIN >= threshold" if (sm - bm) >= THRESH else ("  <- sense ahead" if sm - bm > 0.05 else "")
    print(f"{gn:16} {bstr:28} {sstr:28}  {sm-bm:+.2f}{flag}")

bmean = sum(bo) / len(bo); smean = sum(so) / len(so)
print(f"\noverall cited-recall  baseline {[round(x,2) for x in bo]} mean {bmean:.2f}"
      f"  |  sense {[round(x,2) for x in so]} mean {smean:.2f}  delta {smean-bmean:+.2f}")

# Headline verdict. A reach WIN (any group clears the threshold) is the primary
# axis. When reach TIES, the win may still be real but INVISIBLE to cited-recall:
# on a grep-clean repo both arms reach the full set, and Sense's value shows up as
# fewer round-trips (the sentry case: -46% wall at held recall). Surface it via the
# same efficiency-at-parity gate scoreboard.py uses, so a robust cheaper-at-parity
# tie banks as a ◆ win instead of reading as a draw.
if any(d >= THRESH for d in deltas.values()):
    print("\nVERDICT: WIN")
else:
    from scoreboard import eff_at_parity  # same-dir import; safe (guarded __main__)
    eff = eff_at_parity(beff, seff)
    if eff is not None:
        # eff is not None => walls are non-empty (eff_at_parity gates on that),
        # so median is safe. Wall % is display-only; the gate lives in eff_at_parity.
        wb, ws = statistics.median(beff["walls"]), statistics.median(seff["walls"])
        wall_pct = (ws - wb) / wb * 100.0 if wb else 0.0
        print(f"\nVERDICT: EFFICIENCY-AT-PARITY WIN ◆ — recall tied, sense robustly cheaper: "
              f"billed tokens {eff:+.0f}%, wall {wall_pct:+.0f}% "
              f"(every sense run under every baseline run; sense wall-median lower)")
    else:
        print("\nVERDICT: NOT YET >=threshold — re-author toward the indirect-edge seam, "
              "prove colocated/resolver-gap, or (if recall ties by design) sense is NOT "
              "robustly cheaper here so there is no efficiency-at-parity win either")
