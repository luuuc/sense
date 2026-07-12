#!/usr/bin/env python3
"""Freshness check: do the numbers in each vertical article still match the bench?

Each teardown article carries a `headline:` block (repo, deps_delta, overall_from,
overall_to), an `axes:` block (judge-score numbers: cited_delta, B-score,
related/grounded/contra per arm), and a `data:` pointer to its bench model root
(bench/verticals/<vertical>/results/<model>/). This recomputes those numbers from
the live results (axes via scoreboard.py's readers, the same source `regen via
bench/lib/scoreboard.py` names) and prints FRESH / OUTDATED per article, so a
re-bench can't silently leave stale figures in a draft. It checks NUMBERS only;
the prose is the author's. Articles with neither a `headline:` nor an `axes:`
block (the essay) are skipped. Non-score axes metadata (judge, runs, antifab,
eff_*) stays unchecked.

Usage: check_article_stats.py [articles_dir] [--tol 0.01]
"""
import glob
import json
import os
import sys

try:
    import yaml
except ImportError:  # pragma: no cover - environment guard
    sys.exit("check_article_stats.py: needs pyyaml")

REPO_ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", ".."))
DEFAULT_ARTICLES = os.path.join(
    REPO_ROOT, ".doc", "launch", "02-rails-vertical", "articles")

# axes: keys we can recompute from disk (claim key -> live key). The packs use
# two shapes (related_from/related_to vs related_b/related_s); both map to the
# same live value. Anything not listed here (judge, runs, antifab, eff_*) is
# metadata or informational-only and is not checked.
AXES_KEYS = {
    "cited_delta": "cited_delta",
    "deps_delta": "deps_delta",
    "b_score_from": "b_score_from",
    "b_score_to": "b_score_to",
    "related_from": "related_b",
    "related_b": "related_b",
    "related_to": "related_s",
    "related_s": "related_s",
    "grounded_b": "grounded_b",
    "grounded_s": "grounded_s",
    "contra_b": "contra_b",
    "contra_s": "contra_s",
}


def frontmatter(path):
    """Parse a markdown file's leading YAML frontmatter into a dict."""
    s = open(path).read()
    if not s.startswith("---"):
        return {}
    end = s.find("\n---", 3)
    if end < 0:
        return {}
    try:
        return yaml.safe_load(s[3:end]) or {}
    except yaml.YAMLError:
        return {}


def _runs(repo_dir):
    paths = sorted(glob.glob(os.path.join(repo_dir, "run-*", "scored.json")))
    if not paths and os.path.exists(os.path.join(repo_dir, "scored.json")):
        paths = [os.path.join(repo_dir, "scored.json")]
    return paths


def _mean(xs):
    return sum(xs) / len(xs) if xs else None


def _arm(repo_dir):
    """Mean overall cited-recall and mean dependents-group cited-recall."""
    overall, deps = [], []
    for p in _runs(repo_dir):
        try:
            g = json.load(open(p)).get("gold_recall", {})
        except (OSError, ValueError):
            continue
        overall.append(g.get("cited_recall", 0.0))
        grp = g.get("groups", {}).get("dependents")
        if grp and grp.get("total"):
            deps.append(grp["cited"] / grp["total"])
    return _mean(overall), _mean(deps)


def live_stats(model_root, repo):
    """Recompute the headline numbers for one repo from its bench model root."""
    b_overall, b_deps = _arm(os.path.join(model_root, "baseline", repo))
    s_overall, s_deps = _arm(os.path.join(model_root, "sense", repo))
    deps_delta = (s_deps - b_deps) if (s_deps is not None and b_deps is not None) else None
    return {"overall_from": b_overall, "overall_to": s_overall, "deps_delta": deps_delta}


def live_axes(model_root, repo):
    """Recompute the judge-score axes for one repo via scoreboard's readers
    (the source of truth the packs' `regen via bench/lib/scoreboard.py` names).
    Returns {} when either arm has no runs on disk."""
    import scoreboard as sb  # local: bench/lib is on sys.path for script + tests
    b = sb.arm_axes(model_root, "baseline", repo)
    s = sb.arm_axes(model_root, "sense", repo)
    if b["cited"] is None or s["cited"] is None:
        return {}
    deps = (s["deps"] - b["deps"]) if (
        b["deps"] is not None and s["deps"] is not None) else None
    return {"cited_delta": s["cited"] - b["cited"], "deps_delta": deps,
            "b_score_from": sb.bscore(b), "b_score_to": sb.bscore(s),
            "related_b": b["related"], "related_s": s["related"],
            "grounded_b": b["grounded"], "grounded_s": s["grounded"],
            "contra_b": b["contra"], "contra_s": s["contra"]}


def _fmt(key, v):
    if isinstance(v, int):
        return str(v)
    sign = "+" if key.endswith("_delta") else ""
    return f"{sign}{v:.2f}"


def axes_verdict(claim, live, tol=0.01):
    """Return diffs comparing a claimed axes: block to the live recompute."""
    items = dict(claim)
    if "grounded" in items:  # single-field shape claims both arms
        g = items.pop("grounded")
        items.setdefault("grounded_b", g)
        items.setdefault("grounded_s", g)
    diffs = []
    for key, lkey in AXES_KEYS.items():
        c, got = items.get(key), live.get(lkey)
        if not isinstance(c, (int, float)) or isinstance(c, bool) or got is None:
            continue  # not claimed, metadata, or no live value to compare
        if abs(round(got, 2) - round(c, 2)) > tol:
            diffs.append(f"axes.{key} {_fmt(key, c)}->{_fmt(key, got)}")
    return diffs


def verdict(claim, live, tol=0.01):
    """Return (is_fresh, has_data, diffs) comparing a claimed headline to live."""
    if live["overall_from"] is None and live["overall_to"] is None:
        return (False, False, [])
    diffs = []
    for key in ("deps_delta", "overall_from", "overall_to"):
        c, got = claim.get(key), live.get(key)
        if c is None or got is None:  # metric not claimed, or none live (e.g. no deps group)
            continue
        if abs(round(got, 2) - round(c, 2)) > tol:
            sign = "+" if key == "deps_delta" else ""
            diffs.append(f"{key} {sign}{c:.2f}->{sign}{got:.2f}")
    return (not diffs, True, diffs)


def check(articles_dir, root=REPO_ROOT, tol=0.01):
    """Yield (article_name, status, repo) for every article with a headline
    and/or axes block (gem feeders carry axes only)."""
    rows = []
    for path in sorted(glob.glob(os.path.join(articles_dir, "*.md"))):
        fm = frontmatter(path)
        h = fm.get("headline") if isinstance(fm.get("headline"), dict) else None
        ax = fm.get("axes") if isinstance(fm.get("axes"), dict) else None
        data = fm.get("data")
        if not ((h or ax) and isinstance(data, str)):
            continue
        repo = (h or {}).get("repo") or fm.get("repo")
        model_root = os.path.join(root, data.strip())
        live = live_stats(model_root, repo)
        fresh, has_data, diffs = verdict(h or {}, live, tol)
        if has_data and ax:
            adiffs = axes_verdict(ax, live_axes(model_root, repo), tol)
            diffs += adiffs
            fresh = fresh and not adiffs
        if not has_data:
            status = "NO DATA"
        elif fresh:
            status = "FRESH"
        else:
            status = "OUTDATED  " + "; ".join(diffs)
        rows.append((os.path.basename(path), status, repo))
    return rows


def main(argv):
    articles_dir, tol, rest = DEFAULT_ARTICLES, 0.01, argv[1:]
    i = 0
    while i < len(rest):
        if rest[i] == "--tol":
            tol = float(rest[i + 1])
            i += 2
        else:
            articles_dir = rest[i]
            i += 1
    rows = check(os.path.abspath(articles_dir), tol=tol)
    if not rows:
        print("no articles with a headline: or axes: block under", articles_dir)
        return 0
    w = max(len(r[0]) for r in rows)
    need = sum(1 for _, s, _ in rows if not s == "FRESH")
    for name, status, _ in rows:
        print(f"{name:<{w}}  {status}")
    print(f"\n{len(rows)} checked, {need} need attention")
    return 1 if need else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
