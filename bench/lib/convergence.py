"""Convergence evaluator for the improvement loop.

Reads an iteration's outputs and decides which of the four convergence
criteria hold. Writes `convergence.json` and prints a one-page
"Distance from convergence" block to stdout (the heart of every iteration).

The four criteria (per pitch 20-07):

    1. Score-auditor disagreement rate <5% across 2 consecutive iterations.
    2. Per-scenario tool ranks stable across the 2 iterations (no flips).
    3. Tool discrimination ≥0.10 fairness gap on ≥4 of 6 scenarios.
    4. Held-out validation correlation ≥0.85 with current scoring.

The loop stops cleanly when all four hold for two iterations in a row.

Inputs per iteration directory:
    post-analysis.json                          (criterion 2, 3)
    audit-scoring.<tool>.<repo>.json            (criterion 1)
    validation/held-out-scored.json (optional)  (criterion 4)

Usage:
    python3 bench/lib/convergence.py --iter-dir PATH \\
        [--prev-iter-dir PATH] [--held-out-gold PATH] \\
        [--output PATH]
"""

from __future__ import annotations

import argparse
import glob
import json
import math
import os
import sys
from typing import Any

# --- Thresholds (mirror locked.yaml; encoded here for stdlib independence) ---
DISAGREEMENT_THRESHOLD = 0.05            # criterion 1
DISCRIMINATION_GAP_THRESHOLD = 0.10      # criterion 3
# Criterion 3 wants ≥4 of 6 scenarios above threshold on the full bench.
# When the loop runs on a subset (e.g. `--repo flask,gin,axum`), the
# absolute 4 is mathematically impossible. evaluate_discrimination
# scales this proportionally: min(DISCRIMINATION_MIN_SCENARIOS, 2/3 of
# scenarios present, rounded up).
DISCRIMINATION_MIN_SCENARIOS = 4         # criterion 3 target on full bench
HELD_OUT_CORRELATION_THRESHOLD = 0.85    # criterion 4


# --- IO helpers --------------------------------------------------------------

def _load_json(path: str) -> Any:
    with open(path) as f:
        return json.load(f)


def _maybe_load_json(path: str) -> Any | None:
    return _load_json(path) if os.path.exists(path) else None


# --- Criterion 1: score-auditor disagreement -------------------------------

def evaluate_disagreement(iter_dir: str) -> dict[str, Any]:
    """For each `audit-scoring.<tool>.<repo>.json`, average the per-file
    disagreement_rate. Criterion holds iff the average is below the threshold.
    """
    files = sorted(glob.glob(os.path.join(iter_dir, "audit-scoring.*.json")))
    # Skip the *-full.json variants so we don't double-count.
    files = [f for f in files if not f.endswith("-full.json")]

    if not files:
        return {
            "pass": False,
            "deferred": True,
            "reason": "no audit-scoring.*.json found",
            "rate": None,
            "threshold": DISAGREEMENT_THRESHOLD,
            "per_file": [],
        }

    rates: list[float] = []
    per_file: list[dict[str, Any]] = []
    for f in files:
        try:
            d = _load_json(f)
            r = d.get("disagreement_rate")
            if r is None:
                continue
            rates.append(r)
            per_file.append(
                {
                    "file": os.path.basename(f),
                    "rate": r,
                    "over_threshold": d.get("over_threshold", r > DISAGREEMENT_THRESHOLD),
                }
            )
        except (OSError, json.JSONDecodeError):
            continue

    if not rates:
        return {
            "pass": False,
            "deferred": True,
            "reason": "audit-scoring files lacked disagreement_rate",
            "rate": None,
            "threshold": DISAGREEMENT_THRESHOLD,
            "per_file": per_file,
        }

    avg = sum(rates) / len(rates)
    return {
        "pass": avg < DISAGREEMENT_THRESHOLD,
        "deferred": False,
        "rate": round(avg, 4),
        "threshold": DISAGREEMENT_THRESHOLD,
        "per_file": per_file,
    }


# --- Criterion 2: per-scenario rank stability ------------------------------

def _ranks(post_analysis: dict[str, Any]) -> dict[str, str]:
    """For each repo, return which tool ranks higher: 'sense', 'baseline',
    or 'tie'. Skips repos missing scores.
    """
    out: dict[str, str] = {}
    for repo, info in post_analysis.get("repos", {}).items():
        cs = info.get("current_scores") or {}
        s = cs.get("sense")
        b = cs.get("baseline")
        if s is None or b is None:
            continue
        if s > b:
            out[repo] = "sense"
        elif s < b:
            out[repo] = "baseline"
        else:
            out[repo] = "tie"
    return out


def evaluate_rank_stability(
    iter_dir: str, prev_iter_dir: str | None
) -> dict[str, Any]:
    """Criterion holds iff every shared repo's higher-scoring tool matches
    the previous iteration. No prev iter ⇒ deferred.
    """
    if not prev_iter_dir:
        return {
            "pass": False,
            "deferred": True,
            "reason": "no previous iteration to compare",
            "flips": [],
        }

    cur_pa = _maybe_load_json(os.path.join(iter_dir, "post-analysis.json"))
    prev_pa = _maybe_load_json(os.path.join(prev_iter_dir, "post-analysis.json"))
    if cur_pa is None or prev_pa is None:
        return {
            "pass": False,
            "deferred": True,
            "reason": "post-analysis.json missing in current or previous iter",
            "flips": [],
        }

    cur_ranks = _ranks(cur_pa)
    prev_ranks = _ranks(prev_pa)
    shared = sorted(set(cur_ranks) & set(prev_ranks))

    flips: list[dict[str, str]] = []
    for repo in shared:
        if cur_ranks[repo] != prev_ranks[repo]:
            flips.append(
                {"repo": repo, "prev": prev_ranks[repo], "current": cur_ranks[repo]}
            )

    if len(shared) == 0:
        # post-analysis.json present on both sides but neither yielded
        # any (sense, baseline) score pairs — almost always means
        # analyze-transcripts ran before judge.sh, so current_scores
        # are all None. Surface the cause instead of an empty reason.
        return {
            "pass": False,
            "deferred": True,
            "reason": (
                "post-analysis present on both iters but no repo has "
                "both sense and baseline scores — likely judge hasn't "
                "run yet for the current iter (Phase 3/4 ordering)"
            ),
            "shared_repos": 0,
            "flips": [],
        }

    return {
        "pass": len(flips) == 0,
        "deferred": False,
        "shared_repos": len(shared),
        "flips": flips,
    }


# --- Criterion 3: discrimination -------------------------------------------

def evaluate_discrimination(iter_dir: str) -> dict[str, Any]:
    pa = _maybe_load_json(os.path.join(iter_dir, "post-analysis.json"))
    if pa is None:
        return {
            "pass": False,
            "deferred": True,
            "reason": "post-analysis.json missing",
        }

    repos_above = []
    repos_below = []
    for repo, info in pa.get("repos", {}).items():
        cs = info.get("current_scores") or {}
        gap = cs.get("gap")
        if gap is None:
            continue
        if abs(gap) >= DISCRIMINATION_GAP_THRESHOLD:
            repos_above.append({"repo": repo, "gap": round(gap, 4)})
        else:
            repos_below.append({"repo": repo, "gap": round(gap, 4)})

    total = len(repos_above) + len(repos_below)
    # Scale the required count when running on a subset surface:
    # min(target, ceil(2/3 of present)). Full bench (6 repos) → 4.
    # 3-repo subset → 2. Keeps the criterion meaningful at any surface.
    min_required = (
        DISCRIMINATION_MIN_SCENARIOS
        if total >= DISCRIMINATION_MIN_SCENARIOS
        else (total * 2 + 2) // 3  # ceil(2*total/3)
    )

    return {
        "pass": len(repos_above) >= min_required,
        "deferred": False,
        "scenarios_above_threshold": len(repos_above),
        "scenarios_below_threshold": len(repos_below),
        "min_required": min_required,
        "min_required_full_bench": DISCRIMINATION_MIN_SCENARIOS,
        "gap_threshold": DISCRIMINATION_GAP_THRESHOLD,
        "above": repos_above,
        "below": repos_below,
    }


# --- Criterion 4: held-out validation correlation --------------------------

def _spearman(xs: list[float], ys: list[float]) -> float:
    """Spearman rank correlation. Returns 0.0 for degenerate inputs.

    Stdlib-only — no scipy dependency.
    """
    n = len(xs)
    if n < 2 or len(ys) != n:
        return 0.0

    def ranks(vs: list[float]) -> list[float]:
        # Average rank for ties.
        order = sorted(range(n), key=lambda i: vs[i])
        r = [0.0] * n
        i = 0
        while i < n:
            j = i
            while j + 1 < n and vs[order[j + 1]] == vs[order[i]]:
                j += 1
            avg = (i + j) / 2 + 1
            for k in range(i, j + 1):
                r[order[k]] = avg
            i = j + 1
        return r

    rx = ranks(xs)
    ry = ranks(ys)
    mx = sum(rx) / n
    my = sum(ry) / n
    num = sum((a - mx) * (b - my) for a, b in zip(rx, ry))
    dx = math.sqrt(sum((a - mx) ** 2 for a in rx))
    dy = math.sqrt(sum((b - my) ** 2 for b in ry))
    if dx == 0 or dy == 0:
        return 0.0
    return num / (dx * dy)


def evaluate_held_out(iter_dir: str, gold_paths: list[str]) -> dict[str, Any]:
    """Compare the current iter's held-out re-scored llm_quality against
    the hand-graded gold reference scores via Spearman correlation.

    Expected file layout for the re-scoring step (produced separately):
        iter_dir/validation/held-out-scored.json
    with shape:
        {
          "<tool>/<repo>": {"llm_quality": float, ...},
          ...
        }

    `gold_paths` is a list of `*.gold.json` files (one per held-out
    scenario); each has shape `{"<tool>": {"<criterion>": float, ...}, ...}`.
    For correlation, we collapse each gold scenario to a single mean score
    per tool.
    """
    scored_path = os.path.join(iter_dir, "validation", "held-out-scored.json")
    scored = _maybe_load_json(scored_path)
    if scored is None:
        return {
            "pass": False,
            "deferred": True,
            "reason": f"held-out re-score missing: {scored_path}",
            "correlation": None,
            "threshold": HELD_OUT_CORRELATION_THRESHOLD,
        }
    if not gold_paths:
        return {
            "pass": False,
            "deferred": True,
            "reason": "no gold reference files provided",
            "correlation": None,
            "threshold": HELD_OUT_CORRELATION_THRESHOLD,
        }

    xs: list[float] = []
    ys: list[float] = []
    pairs: list[dict[str, Any]] = []
    for gp in gold_paths:
        repo_slug = os.path.basename(gp).removesuffix(".gold.json")
        try:
            gold = _load_json(gp)
        except (OSError, json.JSONDecodeError):
            continue
        for tool, criteria in gold.items():
            if not isinstance(criteria, dict):
                continue
            numeric = [v for v in criteria.values() if isinstance(v, (int, float))]
            if not numeric:
                continue
            gold_mean = sum(numeric) / len(numeric)
            key = f"{tool}/{repo_slug.split('-')[0]}"
            entry = scored.get(key) or scored.get(f"{tool}/{repo_slug}")
            if entry is None:
                continue
            cur = entry.get("llm_quality")
            if cur is None:
                continue
            xs.append(cur)
            ys.append(gold_mean)
            pairs.append(
                {
                    "tool": tool,
                    "scenario": repo_slug,
                    "current_llm_quality": round(cur, 4),
                    "gold_mean": round(gold_mean, 4),
                }
            )

    if len(xs) < 2:
        return {
            "pass": False,
            "deferred": True,
            "reason": f"only {len(xs)} held-out comparison pair(s) available",
            "correlation": None,
            "threshold": HELD_OUT_CORRELATION_THRESHOLD,
            "pairs": pairs,
        }

    corr = _spearman(xs, ys)
    return {
        "pass": corr >= HELD_OUT_CORRELATION_THRESHOLD,
        "deferred": False,
        "correlation": round(corr, 4),
        "threshold": HELD_OUT_CORRELATION_THRESHOLD,
        "n_pairs": len(xs),
        "pairs": pairs,
    }


# --- Top-level evaluator ----------------------------------------------------

def evaluate(
    iter_dir: str,
    prev_iter_dir: str | None,
    held_out_dir: str | None,
) -> dict[str, Any]:
    gold_paths: list[str] = []
    if held_out_dir:
        gold_paths = sorted(glob.glob(os.path.join(held_out_dir, "*.gold.json")))

    c1 = evaluate_disagreement(iter_dir)
    c2 = evaluate_rank_stability(iter_dir, prev_iter_dir)
    c3 = evaluate_discrimination(iter_dir)
    c4 = evaluate_held_out(iter_dir, gold_paths)

    criteria = {
        "1_auditor_agreement": c1,
        "2_rank_stability": c2,
        "3_discrimination": c3,
        "4_held_out_correlation": c4,
    }
    passes = [c["pass"] for c in criteria.values()]
    deferred = [c.get("deferred", False) for c in criteria.values()]

    return {
        "iter_dir": os.path.abspath(iter_dir),
        "prev_iter_dir": os.path.abspath(prev_iter_dir) if prev_iter_dir else None,
        "criteria": criteria,
        "all_pass": all(passes),
        "any_deferred": any(deferred),
        "pass_count": sum(1 for p in passes if p),
    }


# --- Distance block (stdout, end-of-iteration) ------------------------------

def format_distance_block(result: dict[str, Any]) -> str:
    c = result["criteria"]
    lines = ["## Distance from convergence", ""]

    def tag(d):
        if d.get("deferred"):
            return "—"
        return "✓" if d["pass"] else "✗"

    # 1. Auditor agreement
    c1 = c["1_auditor_agreement"]
    rate = c1.get("rate")
    rate_s = f"{rate*100:.1f}%" if isinstance(rate, (int, float)) else "n/a"
    lines.append(
        f"1. Auditor disagreement <{int(DISAGREEMENT_THRESHOLD*100)}%: "
        f"{tag(c1)} ({rate_s})"
    )

    # 2. Rank stability
    c2 = c["2_rank_stability"]
    if c2.get("deferred"):
        lines.append(f"2. Rank stability vs prev iter: — ({c2.get('reason','')})")
    elif c2["pass"]:
        lines.append(
            f"2. Rank stability vs prev iter: ✓ ({c2['shared_repos']} repos, no flips)"
        )
    else:
        flips_s = ", ".join(
            f"{f['repo']}({f['prev']}→{f['current']})" for f in c2.get("flips", [])
        )
        lines.append(f"2. Rank stability vs prev iter: ✗ ({flips_s})")

    # 3. Discrimination
    c3 = c["3_discrimination"]
    if c3.get("deferred"):
        lines.append(f"3. Discrimination ≥0.10 on ≥4 of 6: — ({c3.get('reason','')})")
    else:
        n = c3["scenarios_above_threshold"]
        total = n + c3["scenarios_below_threshold"]
        lines.append(
            f"3. Discrimination ≥0.10 on ≥{c3['min_required']} of {total}: "
            f"{tag(c3)} ({n}/{total})"
        )

    # 4. Held-out correlation
    c4 = c["4_held_out_correlation"]
    if c4.get("deferred"):
        lines.append(
            f"4. Held-out correlation ≥{HELD_OUT_CORRELATION_THRESHOLD}: "
            f"— ({c4.get('reason','')})"
        )
    else:
        corr = c4.get("correlation")
        corr_s = f"{corr:.2f}" if isinstance(corr, (int, float)) else "n/a"
        lines.append(
            f"4. Held-out correlation ≥{HELD_OUT_CORRELATION_THRESHOLD}: "
            f"{tag(c4)} ({corr_s})"
        )

    lines.append("")
    if result["all_pass"]:
        lines.append("**All four criteria hold this iteration.**")
    else:
        passed = result["pass_count"]
        lines.append(f"**{passed} of 4 criteria pass.**")
    if result["any_deferred"]:
        lines.append(
            "*Some criteria deferred — not enough data yet (first iter, "
            "or held-out not wired).*"
        )
    return "\n".join(lines)


# --- CLI --------------------------------------------------------------------

def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--iter-dir", required=True)
    p.add_argument("--prev-iter-dir", default=None)
    p.add_argument(
        "--held-out-dir",
        default=None,
        help="path to bench/scenarios/held-out/ for *.gold.json discovery",
    )
    p.add_argument("--output", default=None, help="where to write convergence.json")
    p.add_argument(
        "--quiet", action="store_true", help="suppress the Distance block on stdout"
    )
    args = p.parse_args()

    result = evaluate(args.iter_dir, args.prev_iter_dir, args.held_out_dir)

    out_path = args.output or os.path.join(args.iter_dir, "convergence.json")
    with open(out_path, "w") as f:
        json.dump(result, f, indent=2)
        f.write("\n")

    if not args.quiet:
        print(format_distance_block(result))

    return 0 if result["all_pass"] else 1


if __name__ == "__main__":
    sys.exit(main())
