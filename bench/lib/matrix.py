#!/usr/bin/env python3
"""Cross-model matrix for a vertical bench.

A vertical bench is model-scoped: results/vertical/<name>/<model>/<arm>/<repo>/.
The per-model report (reporter.py) compares baseline vs sense within one model;
this aggregates ACROSS models so opus-4-8, gpt-5.4, the ollama-cloud models, ...
sit side by side. For each (model, repo) it reads the mean cited-recall per arm
(averaging run-*/ when present) and reports the sense-over-baseline delta, plus
the discriminator `dependents` group delta when the scenario carries one.

Usage: matrix.py <vertical-root> [--format markdown|json]
  e.g. matrix.py results/vertical/ruby-rails --format markdown
"""
import glob
import json
import os
import sys


def _runs(repo_dir):
    paths = sorted(glob.glob(os.path.join(repo_dir, "run-*", "scored.json")))
    if not paths and os.path.exists(os.path.join(repo_dir, "scored.json")):
        paths = [os.path.join(repo_dir, "scored.json")]
    return paths


def _mean(xs):
    return sum(xs) / len(xs) if xs else None


def _arm_scores(repo_dir):
    """Mean overall cited-recall and mean `dependents`-group cited-recall."""
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


# Price-free consumption metrics (the user explicitly ignores cost_usd, which is
# provider-dependent): wall-clock session time and the token split — billed
# (uncached input + output, what you actually pay regardless of price), cached
# read, and output.
_METRIC_KEYS = ("wall_time_seconds", "token_total_billed", "token_cache_read", "token_output")


def _arm_metrics(repo_dir):
    """Mean of each consumption metric across this arm's runs."""
    acc = {k: [] for k in _METRIC_KEYS}
    for p in _runs(repo_dir):
        try:
            m = json.load(open(p)).get("metrics", {})
        except (OSError, ValueError):
            continue
        for k in _METRIC_KEYS:
            v = m.get(k)
            if v is not None:
                acc[k].append(v)
    return {k: _mean(v) for k, v in acc.items()}


def collect(root):
    """Return {model: {repo: {overall_delta, deps_delta, base, sense}}}."""
    out = {}
    for model in sorted(os.listdir(root)):
        mdir = os.path.join(root, model)
        if not os.path.isdir(mdir) or model.startswith("."):
            continue
        bdir, sdir = os.path.join(mdir, "baseline"), os.path.join(mdir, "sense")
        if not (os.path.isdir(bdir) and os.path.isdir(sdir)):
            continue
        repos = sorted(set(os.listdir(bdir)) & set(os.listdir(sdir)))
        per_repo = {}
        for repo in repos:
            if not os.path.isdir(os.path.join(sdir, repo)):
                continue
            b_overall, b_deps = _arm_scores(os.path.join(bdir, repo))
            s_overall, s_deps = _arm_scores(os.path.join(sdir, repo))
            if b_overall is None or s_overall is None:
                continue
            per_repo[repo] = {
                "baseline_overall": b_overall,
                "sense_overall": s_overall,
                "overall_delta": s_overall - b_overall,
                "deps_delta": (s_deps - b_deps) if (s_deps is not None and b_deps is not None) else None,
                "baseline_metrics": _arm_metrics(os.path.join(bdir, repo)),
                "sense_metrics": _arm_metrics(os.path.join(sdir, repo)),
            }
        if per_repo:
            out[model] = per_repo
    return out


def _fmt_delta(d):
    return "—" if d is None else f"{d:+.2f}"


def _fmt_num(v, integer=True):
    if v is None:
        return "—"
    return f"{round(v):,}" if integer else f"{v:.0f}"


def render_markdown(data, root):
    name = os.path.basename(os.path.normpath(root))
    lines = [f"# {name} — cross-model matrix\n",
             "Sense vs baseline cited-recall by model. `overall Δ` is the whole-scenario "
             "cited-recall lift; `deps Δ` is the discriminator `dependents` group (the headline "
             "where a scenario has one). Each model is benched independently under "
             "`results/vertical/" + name + "/<model>/`.\n"]
    if not data:
        lines.append("_No model results yet._")
        return "\n".join(lines) + "\n"

    # Per-model summary.
    lines.append("## Per-model summary\n")
    lines.append("| model | repos | mean overall Δ | mean deps Δ |")
    lines.append("|---|---|---|---|")
    for model in sorted(data):
        repos = data[model]
        mo = _mean([r["overall_delta"] for r in repos.values()])
        md = _mean([r["deps_delta"] for r in repos.values() if r["deps_delta"] is not None])
        lines.append(f"| {model} | {len(repos)} | {_fmt_delta(mo)} | {_fmt_delta(md)} |")

    # model x repo matrix of overall delta.
    all_repos = sorted({repo for repos in data.values() for repo in repos})
    lines.append("\n## Overall cited-recall Δ (sense − baseline), by model × repo\n")
    lines.append("| model | " + " | ".join(all_repos) + " |")
    lines.append("|---|" + "---|" * len(all_repos))
    for model in sorted(data):
        cells = [_fmt_delta(data[model].get(repo, {}).get("overall_delta")) for repo in all_repos]
        lines.append(f"| {model} | " + " | ".join(cells) + " |")

    # Efficiency (price-free): session time + token consumption, baseline→sense,
    # means across each model's repos. Cost is intentionally excluded.
    lines.append("\n## Efficiency by model (baseline → sense, means across the model's repos)\n")
    lines.append("Session time and token consumption; price-independent (no cost). "
                 "billed = uncached input + output; cached = cache-read; both arms shown as base → sense.\n")
    lines.append("| model | wall s | billed tok | cached tok | output tok | billed Δ% |")
    lines.append("|---|---|---|---|---|---|")
    for model in sorted(data):
        repos = data[model].values()

        def armmean(arm_key, metric):
            return _mean([r[arm_key].get(metric) for r in repos if r[arm_key].get(metric) is not None])

        def pair(metric, intfmt=True):
            b, s = armmean("baseline_metrics", metric), armmean("sense_metrics", metric)
            return f"{_fmt_num(b, intfmt)} → {_fmt_num(s, intfmt)}"

        bb, sb = armmean("baseline_metrics", "token_total_billed"), armmean("sense_metrics", "token_total_billed")
        billed_pct = f"{(sb - bb) / bb * 100:+.0f}%" if bb else "—"
        lines.append(f"| {model} | {pair('wall_time_seconds', False)} | {pair('token_total_billed')} | "
                     f"{pair('token_cache_read')} | {pair('token_output')} | {billed_pct} |")
    return "\n".join(lines) + "\n"


def main(argv):
    if len(argv) < 2:
        sys.exit("usage: matrix.py <vertical-root> [--format markdown|json]")
    root = argv[1]
    fmt = "markdown"
    for i, a in enumerate(argv):
        if a == "--format" and i + 1 < len(argv):
            fmt = argv[i + 1]
    if not os.path.isdir(root):
        sys.exit(f"matrix.py: no such vertical root: {root}")
    data = collect(root)
    if fmt == "json":
        print(json.dumps({"vertical": os.path.basename(os.path.normpath(root)), "models": data}, indent=2))
    else:
        print(render_markdown(data, root), end="")


if __name__ == "__main__":
    main(sys.argv)
