#!/usr/bin/env python3
"""Cross-model matrix for a vertical bench.

A vertical bench is model-scoped: verticals/<name>/results/<model>/<arm>/<repo>/.
The per-model report (reporter.py) compares baseline vs sense within one model;
this aggregates ACROSS models so opus-4-8, gpt-5.6, the ollama-cloud models, ...
sit side by side. For each (model, repo) it reads the mean cited-recall per arm
(averaging run-*/ when present) and reports the sense-over-baseline delta, plus
the discriminator `dependents` group delta when the scenario carries one.

Usage: matrix.py <vertical-root> [--format markdown|json]
  e.g. matrix.py verticals/ruby-rails/results --format markdown
"""
import glob
import json
import os
import sys

import run_validity


def _runs(repo_dir):
    """This arm's scored runs, MEASUREMENTS only (lib/run_validity.measured_runs).

    A run whose harness fell over measured nothing, so averaging it in reports
    the instrument's failure as the arm's score: one 203-char crash stub pulled
    dolt's sense arm from 1.00 to 0.88 and its delta from +0.50 to +0.38. Runs
    the wall clock merely cut short are kept -- a failed exam is still an exam,
    and that truncation asymmetry is the finding.
    """
    return run_validity.measured_runs(repo_dir)


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
# provider-dependent): wall-clock session time and the token split - billed
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
            # A zero wall time is the scorer failing to read one, not a session
            # that took no time: scorer.py takes it from the Claude transcript's
            # `duration_ms`, which the codex and opencode harnesses never emit,
            # so every non-Claude arm published `0 → 0` seconds while run_meta
            # held the real number. Never average that zero in.
            if k == "wall_time_seconds" and not v:
                v = _wall_from_meta(p)
            if v is not None:
                acc[k].append(v)
    return {k: _mean(v) for k, v in acc.items()}


def _wall_from_meta(scored_path):
    """This run's wall time as its runner recorded it, or None."""
    meta_path = os.path.join(os.path.dirname(scored_path), "run_meta.json")
    try:
        with open(meta_path) as f:
            return json.load(f).get("wall_time_seconds") or None
    except (OSError, ValueError):
        return None


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
                # Per-arm measured-run counts, so the published repeatability
                # claim is derived rather than asserted.
                "runs": [len(_runs(os.path.join(bdir, repo))),
                         len(_runs(os.path.join(sdir, repo)))],
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


def _judge_models(root):
    """Every judge model that actually graded a run under this root."""
    seen = set()
    for p in glob.glob(os.path.join(root, "**", "judged.json"), recursive=True):
        # Off-board runs (dryrun probes, parked cells) must not make the board
        # look judge-split: two sonnet-graded pebble PROBES kept the published
        # report warning about a split that the campaign no longer had.
        if run_validity.is_parked(os.path.relpath(p, root)):
            continue
        try:
            with open(p) as f:
                judge = json.load(f).get("judge") or {}
        except (OSError, ValueError):
            continue
        if judge.get("model"):
            seen.add(judge["model"])
    return sorted(seen)


def _grading_paragraph(root):
    """The Grading paragraph, with the judge named FROM THE DATA.

    Naming it in prose let it drift unnoticed: the go board published
    "Claude Sonnet 4.6" while four of five arms had in fact been graded by
    Opus 4.7, and one cell by both.
    """
    models = _judge_models(root)
    if len(models) == 1:
        who = f"A separate judge model ({models[0]})"
        caveat = ""
    elif models:
        who = "A separate judge model"
        caveat = (" Those runs were NOT all graded by the same judge - **{}** - which is a "
                  "known inconsistency on this board, not a design: swapping judge models "
                  "invalidates comparison between the runs they graded.".format(
                      ", ".join(models)))
    else:
        who, caveat = "A separate judge model", ""
    return (f"**Grading.** {who} grades each answer's coverage against the authored "
            "must-find set, so a confident-sounding but incomplete answer is penalised "
            "for what it leaves out. Every `path:line` an answer prints is then checked "
            "against the repo at the benchmarked commit; any citation that does not "
            "resolve is listed per model in the [citation check](#per-model-reports)."
            f"{caveat}\n")


def _repeatability_sentence(data):
    """State the real run counts. "Run more than once" was false for every x1
    confirmation arm, and the RUNS=2 law binds the headline arm only."""
    counts = {n for repos in data.values() for repo_data in repos.values()
              for n in repo_data.get("runs", [])}
    counts.discard(0)
    if not counts:
        return "Run counts vary by arm."
    lo, hi = min(counts), max(counts)
    if lo == hi:
        return f"Each (model, repo) pair was run {lo}x."
    return (f"Run counts vary by arm ({lo}x to {hi}x): the headline arm carries the "
            f"RUNS=2 law, while cross-model confirmation arms run 1x by design and "
            f"their numbers are directional, carrying an OPEN flag.")


def _fmt_delta(d):
    return "-" if d is None else f"{d:+.2f}"


def _fmt_num(v, integer=True):
    if v is None:
        return "-"
    return f"{round(v):,}" if integer else f"{v:.0f}"


def render_markdown(data, root):
    # The vertical's results dir is `verticals/<vertical>/results`, so the basename
    # is the generic "results"; the meaningful name is the parent. Fall back to the
    # basename for any other layout.
    base = os.path.basename(os.path.normpath(root))
    vertical = os.path.basename(os.path.dirname(os.path.normpath(root))) if base == "results" else base
    lines = [f"# {vertical} - Sense vertical benchmark\n",
             "This is the benchmark, the methodology, and the raw data behind the "
             f"{vertical} write-ups: how much a structural code index (**Sense**) helps an AI "
             "coding agent answer questions about real-world codebases in this stack, measured "
             "across several models.\n",
             "Every scenario is run twice with the same model: a **baseline** arm (the agent's "
             "normal tools) and a **sense** arm (the same tools plus the Sense index). Each "
             "scenario declares a must-find set of code locations, and the score is **cited "
             "recall** - the share of that set the answer pinned to an exact `path:line`. The "
             "deltas below are sense minus baseline, so **positive means Sense helped**.\n",
             "Jump to: [Methodology](#methodology) · [Results](#results) · "
             "[Per-model reports](#per-model-reports) · [Per-repo variance](#per-repo-variance)\n"]
    if not data:
        lines.append("_No model results yet._")
        return "\n".join(lines) + "\n"

    n_models = len(data)
    n_repos = len({repo for repos in data.values() for repo in repos})

    # ── Methodology ──────────────────────────────────────────────────
    lines.append("## Methodology\n")
    lines.append(
        "**The question.** Does giving an AI coding agent a structural index of a codebase make "
        "it answer questions about that code more completely and more precisely? Sense is that "
        "index: it maps a repo's symbols, call relationships, and dependents so the agent can "
        "look them up instead of reading files one at a time.\n")
    lines.append(
        "**The two arms.** Every scenario runs twice with the *same* model and the *same* "
        "underlying toolkit. The **baseline** arm uses the agent's normal tools (file reads, "
        "grep, and so on). The **sense** arm adds the Sense index on top. Nothing else changes, "
        "so any gap between the two is attributable to the index.\n")
    lines.append(
        f"**The repositories.** The scenarios run against {n_repos} real-world codebases from "
        "this stack, each pinned to a fixed commit so a run is reproducible. They span small "
        "libraries to large applications, including ones far too big to fit in a single context "
        "window.\n")
    lines.append(
        "**The scenarios.** Each scenario is a realistic, multi-step comprehension task (for "
        "example: trace a request from its controller through to persistence and locate the "
        "tests that cover it). Each one declares a **must-find set** - the exact code locations "
        "a complete, correct answer should surface. Scenarios are written so that a naive text "
        "search does not trivially answer them: the relevant code is scattered across "
        "non-obvious places.\n")
    lines.append(
        "**The metrics.** The headline is **cited recall**: of the must-find set, the share the "
        "answer pinned to an exact `path:line` an agent could jump straight to. Reported "
        "alongside it are **mention recall** (named at all, location optional), **relationship "
        "correctness** (states the right connection, not just the name), **truthfulness** (no "
        "confidently false claims), and **billed tokens** (the context the answer cost to "
        "produce). Recall is the goal; tokens are reported but never traded against it.\n")
    lines.append(_grading_paragraph(root))
    lines.append(
        f"**Repeatability.** {_repeatability_sentence(data)} The run-to-run spread is published "
        "under [Per-repo variance](#per-repo-variance), so a headline number is trusted only "
        "when it is stable rather than a lucky draw.\n")

    # ── Results (raw data) ───────────────────────────────────────────
    lines.append("## Results\n")
    lines.append(
        f"The raw numbers, {n_models} models across {n_repos} repos. Each model's full per-repo "
        "tables are linked under [Per-model reports](#per-model-reports).\n")

    # Per-model summary.
    lines.append("### Per-model summary\n")
    lines.append(
        "One row per model. **repos** is how many of the vertical's scenarios it was benched on; "
        "the two Δ columns are the mean cited-recall lift (sense − baseline) across them - "
        "**overall** for the whole scenario, **deps** for the harder `dependents` group (what "
        "depends on a given symbol). Positive means Sense helped that model on average.\n")
    lines.append("| model | repos | mean overall Δ | mean deps Δ |")
    lines.append("|---|---|---|---|")
    for model in sorted(data):
        repos = data[model]
        mo = _mean([r["overall_delta"] for r in repos.values()])
        md = _mean([r["deps_delta"] for r in repos.values() if r["deps_delta"] is not None])
        lines.append(f"| {model} | {len(repos)} | {_fmt_delta(mo)} | {_fmt_delta(md)} |")

    # model x repo matrix of overall delta.
    all_repos = sorted({repo for repos in data.values() for repo in repos})
    lines.append("\n### Overall cited-recall Δ (sense − baseline), by model × repo\n")
    lines.append(
        "Every cell is the cited-recall lift for one model on one repo. For example, `+0.40` "
        "means the sense arm pinned 40 percentage points more of that repo's must-find set to an "
        "exact location than the baseline did. A near-zero value is a tie; a `-` means that repo "
        "was not benched for that model.\n")
    lines.append("| model | " + " | ".join(all_repos) + " |")
    lines.append("|---|" + "---|" * len(all_repos))
    for model in sorted(data):
        cells = [_fmt_delta(data[model].get(repo, {}).get("overall_delta")) for repo in all_repos]
        lines.append(f"| {model} | " + " | ".join(cells) + " |")

    # Efficiency (price-free): session time + token consumption, baseline→sense,
    # means across each model's repos. Cost is intentionally excluded.
    lines.append("\n### Efficiency by model (baseline → sense)\n")
    lines.append(
        "What each arm spent to produce its answers, averaged across the model's repos and shown "
        "as baseline → sense. These are consumption figures, independent of any provider's price "
        "(no dollar cost). **billed** is the tokens you actually pay for (uncached input + "
        "output); **cached** is cache-read context; **wall s** is session wall-clock seconds. "
        "Lower is cheaper - but recall is never traded for a smaller token bill, so read this "
        "alongside the lift above, not instead of it.\n")
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
        billed_pct = f"{(sb - bb) / bb * 100:+.0f}%" if bb else "-"
        lines.append(f"| {model} | {pair('wall_time_seconds', False)} | {pair('token_total_billed')} | "
                     f"{pair('token_cache_read')} | {pair('token_output')} | {billed_pct} |")

    # Per-model detailed reports. Each model dir carries a full per-repo report
    # (baseline vs sense tables, the aggregate, process efficiency) and a list of
    # any citations that did not resolve against the repo checkout.
    lines.append("\n## Per-model reports\n")
    lines.append("Full per-repo tables and the citation check for each model:\n")
    lines.append("| model | report | citation check |")
    lines.append("|---|---|---|")
    for model in sorted(data):
        report_link = (f"[report.md]({model}/report.md)"
                       if os.path.exists(os.path.join(root, model, "report.md")) else "-")
        cite_link = (f"[citation-hallucinations.md]({model}/citation-hallucinations.md)"
                     if os.path.exists(os.path.join(root, model, "citation-hallucinations.md")) else "-")
        lines.append(f"| {model} | {report_link} | {cite_link} |")

    # Per-repo variance (run-to-run spread per repo, across models) when present.
    var_files = sorted(glob.glob(os.path.join(root, "variance", "*.md")))
    if var_files:
        lines.append("\n## Per-repo variance\n")
        lines.append("Run-to-run spread per repo (is the headline stable or noise?):\n")
        links = [f"[{os.path.splitext(os.path.basename(p))[0]}](variance/{os.path.basename(p)})"
                 for p in var_files]
        lines.append(" · ".join(links))

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
