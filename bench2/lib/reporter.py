#!/usr/bin/env python3
"""Aggregate scenario benchmark results into comparison tables.

Usage: reporter.py <results_dir> [--format terminal|markdown|json]

Reads scored.json files from results/<tool>/<repo>/ (one per scenario).
Produces per-scenario comparison tables and an aggregate ranking.
"""

import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fairness


SCENARIO_DESCRIPTIONS = {
    "flask": "Multi-step Flask refactoring: trace WSGI dispatch, locate tests, add a debug parameter, verify the change. Tests call graph traversal, test-file mapping, and safe code modification awareness.",
    "gin": "Multi-step Gin exploration: understand middleware chaining, trace HTTP dispatch, find dead code, modify the recovery middleware. Tests data flow tracing, dead code detection, and structural editing awareness.",
    "axum": "Multi-step Axum refactoring: trace Handler trait propagation, understand extractor chaining, add a request ID layer. Tests Rust trait analysis, Tower middleware comprehension, and layered modification.",
    "discourse": "Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.",
    "javalin": "Multi-step Javalin exploration: understand servlet dispatch, trace routing table construction, add a custom error handler. Tests Java framework comprehension and handler registration patterns.",
    "nextjs": "Multi-step Next.js exploration: trace SSR render path, understand route matching, thread a request ID. Tests TypeScript monorepo navigation and complex server-side pipeline understanding.",
}


METRIC_DIRECTIONS = {
    "fairness": ("higher", "Combined fairness score — 0.10·keyword_coverage + 0.55·llm_quality + 0.15·citation_grounding + 0.20·efficiency. Shown as `—` if judge.sh has not run yet."),
    "adoption": ("higher", "Adoption score — tool fluency + discoverability, for code-intel comparisons only"),
    "keyword_coverage": ("higher", "Hit rate across keyword smoke-test checks (sum of hits / sum of totals; bonus weighted 0.5). Now a 10% smoke test, not the headline."),
    "llm_quality": ("higher", "Judge-rated answer quality, per-scenario mean of step_quality. 0.55 weight in fairness — the headline."),
    "efficiency": ("higher", "Half token efficiency + half time efficiency, each calibrated per repo"),
    "tokens": ("lower", "Billed tokens (uncached) — lower is better (cheaper)"),
    "wall_time": ("lower", "Wall-clock time — lower is better, folded into efficiency"),
    "cost_usd": ("lower", "API cost in USD — lower is better"),
    "cites": ("higher", "Citations grounded against the repo checkout: `grounded/total`. A trailing **!N** marks line numbers beyond EOF — outright fabrication. Folded into fairness at 15%."),
}


def _attach_fairness(result, result_dir):
    """Load judged.json from result_dir and stamp fairness onto result.

    The combined fairness score lives only in memory here — neither
    scored.json nor judged.json carries it on its own. If judged.json
    is missing, fairness_score stays None and the report renders `—`.
    """
    judged_path = os.path.join(result_dir, "judged.json")
    judged = None
    if os.path.exists(judged_path):
        try:
            with open(judged_path) as f:
                judged = json.load(f)
        except (json.JSONDecodeError, OSError):
            judged = None

    f = fairness.compute(result, judged)
    result["fairness_score"] = f["score"]
    result["fairness_complete"] = f["complete"]
    result["llm_quality"] = (
        f["components"]["llm_quality"] if f["components"]["llm_quality"] is not None else None
    )
    if judged is not None:
        result["_judge_steps"] = judged.get("steps", [])


def load_results(results_dir):
    results = []
    for tool in sorted(os.listdir(results_dir)):
        tool_dir = os.path.join(results_dir, tool)
        if not os.path.isdir(tool_dir) or tool.startswith("."):
            continue
        for repo in sorted(os.listdir(tool_dir)):
            repo_dir = os.path.join(tool_dir, repo)
            if not os.path.isdir(repo_dir):
                continue

            scored_path = os.path.join(repo_dir, "scored.json")
            if os.path.exists(scored_path):
                with open(scored_path) as f:
                    result = json.load(f)

                result["tool"] = tool
                result["repo_key"] = repo
                _attach_fairness(result, repo_dir)

                meta_path = os.path.join(repo_dir, "run_meta.json")
                if os.path.exists(meta_path):
                    with open(meta_path) as f:
                        meta = json.load(f)
                    result["_tool_version"] = meta.get("tool_version")
                    result["_repo_commit"] = meta.get("repo_commit")
                    result["_timestamp"] = meta.get("timestamp")

                for run_entry in sorted(os.listdir(repo_dir)):
                    if run_entry.startswith("run-"):
                        run_dir = os.path.join(repo_dir, run_entry)
                        run_scored = os.path.join(run_dir, "scored.json")
                        if os.path.exists(run_scored):
                            with open(run_scored) as f:
                                r2 = json.load(f)
                            r2["tool"] = tool
                            r2["repo_key"] = repo
                            r2["_run"] = run_entry
                            _attach_fairness(r2, run_dir)
                            results.append(r2)
                        continue

                results.append(result)

    return results


def build_per_scenario_table(results):
    """Build one table per repo (scenario), tools as rows."""
    by_repo = {}
    for r in results:
        repo = r.get("repo_key", r.get("repo", "unknown"))
        by_repo.setdefault(repo, {})[r["tool"]] = r

    tables = []
    for repo in sorted(by_repo.keys()):
        tool_rows = by_repo[repo]
        rows = []
        for tool, r in sorted(tool_rows.items()):
            m = r.get("metrics", {})

            cg = r.get("citation_grounding") or {}
            rows.append({
                "tool": tool,
                "fairness_score": r.get("fairness_score"),
                "fairness_complete": r.get("fairness_complete", False),
                "adoption_score": r.get("adoption_score"),
                "keyword_coverage": r.get("keyword_coverage"),
                "llm_quality": r.get("llm_quality"),
                "efficiency": r.get("efficiency"),
                "tokens": m.get("token_total_billed", m.get("token_total", 0)),
                "wall_time": m.get("wall_time_seconds"),
                "cost_usd": m.get("cost_usd"),
                "cost_estimated": bool(m.get("cost_estimated")),
                "failed": bool(r.get("failed")),
                "failure_reason": r.get("failure_reason"),
                "cites_grounded": cg.get("grounded"),
                "cites_total": cg.get("total"),
                "cites_hallucinated": cg.get("hallucinated", 0),
            })
        rows.sort(key=lambda r2: (r2["fairness_score"] or 0), reverse=True)
        tables.append({"repo": repo, "rows": rows})
    return tables


def build_aggregate(results):
    by_tool = {}
    for r in results:
        tool = r["tool"]
        by_tool.setdefault(tool, []).append(r)

    agg = []
    for tool_name in sorted(by_tool.keys()):
        runs = by_tool[tool_name]
        n = len(runs)
        fairness_scores = [r2.get("fairness_score") for r2 in runs if r2.get("fairness_score") is not None]
        adoption = [r2.get("adoption_score") for r2 in runs if r2.get("adoption_score") is not None]
        keyword = [r2.get("keyword_coverage") for r2 in runs if r2.get("keyword_coverage") is not None]
        quality = [r2.get("llm_quality") for r2 in runs if r2.get("llm_quality") is not None]
        effs = [r2.get("efficiency") for r2 in runs if r2.get("efficiency") is not None]
        tokens = [r2.get("metrics", {}).get("token_total_billed", r2.get("metrics", {}).get("token_total", 0)) for r2 in runs]
        times = [r2.get("metrics", {}).get("wall_time_seconds") for r2 in runs
                 if r2.get("metrics", {}).get("wall_time_seconds") is not None]
        costs = [r2.get("metrics", {}).get("cost_usd") for r2 in runs
                 if r2.get("metrics", {}).get("cost_usd") is not None]

        # Sum citation counts so the aggregate rate is the true pooled
        # rate (grounded/total across all scenarios), not the mean of
        # per-scenario rates. A scenario with 0/0 citations contributes
        # nothing — it doesn't drag the average down to "0%".
        total_cites = sum((r2.get("citation_grounding") or {}).get("total", 0) for r2 in runs)
        grounded_cites = sum((r2.get("citation_grounding") or {}).get("grounded", 0) for r2 in runs)
        hallucinated_cites = sum((r2.get("citation_grounding") or {}).get("hallucinated", 0) for r2 in runs)

        def avg(lst):
            return sum(lst) / len(lst) if lst else 0.0

        failures = sum(1 for r2 in runs if r2.get("failed"))
        agg.append({
            "tool": tool_name,
            "scenarios": n,
            "failures": failures,
            "avg_fairness": round(avg(fairness_scores), 4) if fairness_scores else None,
            "avg_adoption": round(avg(adoption), 4) if adoption else None,
            "avg_keyword_coverage": round(avg(keyword), 4) if keyword else None,
            "avg_llm_quality": round(avg(quality), 4) if quality else None,
            "avg_efficiency": round(avg(effs), 4) if effs else None,
            "avg_tokens": round(avg(tokens)),
            "avg_time": round(avg(times), 1) if times else None,
            "total_cost": round(sum(costs), 2) if costs else None,
            "cites_grounded": grounded_cites,
            "cites_total": total_cites,
            "cites_hallucinated": hallucinated_cites,
            "avg_grounding": round(grounded_cites / total_cites, 4) if total_cites > 0 else None,
        })
    agg.sort(key=lambda r2: (r2["avg_fairness"] or 0), reverse=True)
    return agg


def build_step_detail(results):
    """Per-step breakdown for each scenario."""
    by_repo = {}
    for r in results:
        repo = r.get("repo_key", r.get("repo", "unknown"))
        by_repo.setdefault(repo, {}).setdefault(r["tool"], r)

    detail = []
    for repo in sorted(by_repo.keys()):
        tool_rows = by_repo[repo]
        steps_names = []
        tool_steps = {}

        for tool, r_obj in sorted(tool_rows.items()):
            for step in r_obj.get("steps", []):
                if step["name"] not in steps_names:
                    steps_names.append(step["name"])
                tool_steps.setdefault(tool, {})[step["name"]] = {
                    "score": step.get("combined_score"),
                    "hits": step.get("hits_required"),
                    "total": step.get("total_required"),
                }

        detail.append({
            "repo": repo,
            "steps": steps_names,
            "tools": tool_steps,
        })
    return detail


def _fmt_cites_md(row):
    total = row.get("cites_total")
    if not total:
        return "—"
    grounded = row.get("cites_grounded") or 0
    halluc = row.get("cites_hallucinated") or 0
    s = f"{grounded}/{total}"
    if halluc:
        s += f" (**!{halluc}**)"
    return s


def _fmt_cites(row, width=7):
    """Format the per-scenario Cites cell.

    `grounded/total`, with a trailing `!N` if N citations were
    out-of-range line numbers (the hard "made up" signal). 0/0 prints
    as a dash — the answer simply had no structured citations to check,
    which is informational not bad.
    """
    total = row.get("cites_total")
    if not total:
        return f"{'—':>{width}}"
    grounded = row.get("cites_grounded") or 0
    halluc = row.get("cites_hallucinated") or 0
    s = f"{grounded}/{total}"
    if halluc:
        s += f"!{halluc}"
    return f"{s:>{width}}"


def _rank_badge(i):
    if i == 0:
        return " :1st_place_medal:"
    if i == 1:
        return " :2nd_place_medal:"
    if i == 2:
        return " :3rd_place_medal:"
    return ""


# ── Terminal ─────────────────────────────────────────────────────────


def format_terminal(tables, aggregate, header):
    lines = []
    lines.append(f"Scenario Evaluation — {sum(t['rows'][0]['fairness_score'] is not None for t in tables if t['rows'])} completed")
    lines.append("")

    for table in tables:
        repo = table["repo"]
        desc = SCENARIO_DESCRIPTIONS.get(repo, "")
        lines.append(f"{'=' * 78}")
        lines.append(f"  {repo}")
        lines.append(f"{'=' * 78}")
        if desc:
            lines.append(f"  {desc}")
        lines.append("")
        hdr = f"  {'Tool':<14} {'Fair':>6} {'Adopt':>6} {'KWcov':>6} {'LLMQ':>5} {'Eff':>5} {'Tokens':>8} {'Time':>7} {'Cost':>7} {'Cites':>7}"
        lines.append(hdr)
        lines.append(f"  {'-' * 86}")

        for row in table["rows"]:
            if row.get("failed"):
                wt = f"{row['wall_time']:>6.1f}s" if row["wall_time"] else "      —"
                reason = row.get("failure_reason") or "failed"
                lines.append(
                    f"  {row['tool']:<14} FAILED                                              {wt}   ({reason})"
                )
                continue
            fa = f"{row['fairness_score']:>6.3f}" if row["fairness_score"] is not None else "     —"
            ad = f"{row['adoption_score']:>6.3f}" if row["adoption_score"] is not None else "     —"
            kw = f"{row['keyword_coverage']:>6.0%}" if row["keyword_coverage"] is not None else "     —"
            lq = f"{row['llm_quality']:>5.2f}" if row["llm_quality"] is not None else "    —"
            ef = f"{row['efficiency']:>5.2f}" if row["efficiency"] is not None else "    —"
            tk = f"{row['tokens']:>8,}"
            wt = f"{row['wall_time']:>6.1f}s" if row["wall_time"] else "      —"
            co = f"${row['cost_usd']:>6.2f}" if row["cost_usd"] is not None else "      —"
            ci = _fmt_cites(row, width=7)
            lines.append(
                f"  {row['tool']:<14} {fa}  {ad} {kw} {lq} {ef} {tk} {wt} {co} {ci}"
            )
        lines.append("")

    lines.append(f"{'=' * 78}")
    lines.append("  AGGREGATE (all scenarios)")
    lines.append(f"{'=' * 78}")
    lines.append("")
    lines.append(f"  {'Tool':<14} {'Runs':>4} {'Fail':>4} {'Avg Fair':>8} {'Avg Adopt':>9} {'AvgKWcov':>8} {'AvgLLMQ':>7} {'Avg Eff':>7} {'Avg Tokens':>10} {'Avg Time':>9} {'Cost':>7} {'Cites':>9}")
    lines.append(f"  {'-' * 110}")

    for row in aggregate:
        af = f"{row['avg_fairness']:>8.4f}" if row["avg_fairness"] is not None else "       —"
        aa = f"{row['avg_adoption']:>9.4f}" if row["avg_adoption"] is not None else "        —"
        akw = f"{row['avg_keyword_coverage']:>8.1%}" if row["avg_keyword_coverage"] is not None else "       —"
        alq = f"{row['avg_llm_quality']:>7.4f}" if row["avg_llm_quality"] is not None else "      —"
        ae = f"{row['avg_efficiency']:>7.4f}" if row["avg_efficiency"] is not None else "      —"
        at = f"{row['avg_time']:>8.1f}s" if row.get("avg_time") is not None else "        —"
        co = f"${row['total_cost']:>6.2f}" if row["total_cost"] else "      —"
        ag = f"{row['avg_grounding']:>9.1%}" if row.get("avg_grounding") is not None else "        —"
        lines.append(
            f"  {row['tool']:<14} {row['scenarios']:>4} {row.get('failures', 0):>4} {af} {aa} {akw} {alq} {ae} {row['avg_tokens']:>10,} {at} {co} {ag}"
        )

    lines.append("")
    return "\n".join(lines)


# ── Markdown ─────────────────────────────────────────────────────────


def format_markdown(tables, aggregate, header):
    lines = []
    lines.append("## Scenario Evaluation")
    lines.append("")
    lines.append(f"Results: {len(aggregate)} tools × {len(tables)} scenarios")
    lines.append("")
    lines.append("Two-layer scoring: **Fairness** = 0.10·keyword_coverage + 0.55·llm_quality + 0.15·citation_grounding + 0.20·efficiency. The judge layer (llm_quality) is the 55% headline — keyword overlap dropped to a 10% smoke test. Fairness cells render `—` if `judge.sh` has not been run on a result. **Adoption** (tool fluency + discoverability) is for code-intel-vs-code-intel comparisons only.")
    lines.append("")
    lines.append("**Citations** are `file.ext:line` or `file.ext:Symbol` references the assistant printed in its answer. The scorer checks each one against the repo at `run_meta.repo_commit`. A `0/0` Cites cell means the answer had no structured citations to verify — neither penalized nor rewarded; prose-only claims are scored by `llm_quality` instead. The full list of ungrounded citations lives in [`citation-hallucinations.md`](citation-hallucinations.md).")
    lines.append("")

    lines.append("### Reading the scores")
    lines.append("")
    lines.append("| Metric | Best | Meaning |")
    lines.append("|--------|------|---------|")
    for metric, (direction, meaning) in METRIC_DIRECTIONS.items():
        arrow = "Higher" if direction == "higher" else "Lower"
        lines.append(f"| {metric} | {arrow} | {meaning} |")
    lines.append("")

    for table in tables:
        repo = table["repo"]
        desc = SCENARIO_DESCRIPTIONS.get(repo, "")
        lines.append(f"### {repo}")
        lines.append("")
        if desc:
            lines.append(f"> {desc}")
            lines.append("")
        lines.append("| Rank | Tool | Fairness | Adoption | Keyword Cov. | LLM Quality | Efficiency | Tokens | Time | Cost | Cites |")
        lines.append("|-----:|------|--------:|---------:|------------:|------------:|---------:|-------:|-----:|-----:|------:|")

        for i, row in enumerate(table["rows"]):
            badge = _rank_badge(i)
            if row.get("failed"):
                wt = f"{row['wall_time']:.1f}s" if row["wall_time"] else "—"
                if row.get("cost_usd") is not None:
                    co = f"~${row['cost_usd']:.2f}*" if row.get("cost_estimated") else f"${row['cost_usd']:.2f}"
                else:
                    co = "—"
                reason = row.get("failure_reason") or "failed"
                lines.append(
                    f"| {i+1} | {row['tool']} | **FAILED** | — | — | — | — | — | {wt} | {co} | — |"
                    f" <!-- {reason} -->"
                )
                continue
            fa = f"{row['fairness_score']:.3f}" if row["fairness_score"] is not None else "—"
            ad = f"{row['adoption_score']:.3f}" if row["adoption_score"] is not None else "—"
            kw = f"{row['keyword_coverage']:.0%}" if row["keyword_coverage"] is not None else "—"
            lq = f"{row['llm_quality']:.2f}" if row["llm_quality"] is not None else "—"
            ef = f"{row['efficiency']:.2f}" if row["efficiency"] is not None else "—"
            tk = f"{row['tokens']:,}"
            wt = f"{row['wall_time']:.1f}s" if row["wall_time"] else "—"
            co = f"${row['cost_usd']:.2f}" if row["cost_usd"] is not None else "—"
            ci = _fmt_cites_md(row)
            lines.append(
                f"| {i+1} | {row['tool']}{badge} | {fa} | {ad} | {kw} | {lq} | {ef} | {tk} | {wt} | {co} | {ci} |"
            )
        lines.append("")

    lines.append("### Aggregate")
    lines.append("")
    lines.append("Failed runs count as fairness 0 in the average. The `Failures` column shows how many scenarios the tool could not complete. Costs marked with `*` are estimated from per-message token usage in the partial transcript, because the session never emitted a final cost event.")
    lines.append("")
    lines.append("| Rank | Tool | Scenarios | Failures | Avg Fairness | Avg Adoption | Avg Keyword Cov. | Avg LLM Quality | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |")
    lines.append("|-----:|------|----------:|--------:|------------:|-----------:|---------------:|---------------:|--------------:|-----------:|--------:|-----------:|--------------:|")
    for i, row in enumerate(aggregate):
        badge = _rank_badge(i)
        af = f"{row['avg_fairness']:.4f}" if row["avg_fairness"] is not None else "—"
        aa = f"{row['avg_adoption']:.4f}" if row["avg_adoption"] is not None else "—"
        akw = f"{row['avg_keyword_coverage']:.4f}" if row["avg_keyword_coverage"] is not None else "—"
        alq = f"{row['avg_llm_quality']:.4f}" if row["avg_llm_quality"] is not None else "—"
        ae = f"{row['avg_efficiency']:.4f}" if row.get("avg_efficiency") is not None else "—"
        at = f"{row['avg_time']:.1f}s" if row.get("avg_time") is not None else "—"
        co = f"${row['total_cost']:.2f}" if row["total_cost"] else "—"
        if row.get("avg_grounding") is not None:
            ag = f"{row['avg_grounding']:.1%} ({row.get('cites_grounded', 0)}/{row.get('cites_total', 0)})"
            if row.get("cites_hallucinated"):
                ag += f" **!{row['cites_hallucinated']}**"
        else:
            ag = "—"
        fails = row.get("failures", 0)
        fail_cell = f"**{fails}**" if fails else "0"
        lines.append(
            f"| {i+1} | {row['tool']}{badge} | {row['scenarios']} | {fail_cell} | {af} | {aa} | {akw} | {alq} | {ae} |"
            f" {row['avg_tokens']:,} | {at} | {co} | {ag} |"
        )
    lines.append("")

    return "\n".join(lines)


def format_json(tables, aggregate, header):
    return json.dumps({"tables": tables, "aggregate": aggregate}, indent=2)


# ── Citation hallucination log ───────────────────────────────────────


def format_hallucination_log(results):
    """Render `citation-hallucinations.md` from scored results.

    Grouped tool → scenario. Hallucinated (out-of-range line numbers)
    is the smoking gun, listed first. Unresolved (missing file or
    symbol not near cited line) is softer and listed second.
    """
    lines = ["# Citation hallucinations", ""]
    lines.append(
        "Citations the assistant printed that did not resolve against the "
        "repo checked out at `run_meta.repo_commit`. "
        "**Hallucinated** = line number beyond EOF (made-up number). "
        "**Unresolved** = file not in repo, or symbol not within ±5 lines "
        "of the cited line."
    )
    lines.append("")
    lines.append("Not yet folded into the fairness score — see pitch 20-04.")
    lines.append("")

    by_tool = {}
    for r in results:
        by_tool.setdefault(r["tool"], []).append(r)

    for tool in sorted(by_tool):
        lines.append(f"## {tool}")
        lines.append("")
        any_problems = False
        for r in sorted(by_tool[tool], key=lambda r2: r2.get("repo_key", "")):
            cg = r.get("citation_grounding") or {}
            details = cg.get("details", [])
            halluc = [d for d in details if d.get("status") == "hallucinated"]
            unres = [d for d in details if d.get("status") == "unresolved"]
            if not (halluc or unres):
                continue
            any_problems = True
            repo = r.get("repo_key", r.get("repo", "?"))
            grounded = cg.get("grounded", 0)
            total = cg.get("total", 0)
            lines.append(f"### {tool}/{repo}  — {grounded}/{total} grounded")
            lines.append("")
            if halluc:
                lines.append("**Hallucinated**")
                for d in halluc:
                    lines.append(f"- `{d['file']}:{d['locator']}` — {d['reason']}")
                lines.append("")
            if unres:
                lines.append("**Unresolved**")
                for d in unres:
                    lines.append(f"- `{d['file']}:{d['locator']}` — {d['reason']}")
                lines.append("")

        if not any_problems:
            lines.append("_No ungrounded citations._")
            lines.append("")

    return "\n".join(lines)


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: reporter.py <results_dir> [--format terminal|markdown|json]", file=sys.stderr)
        sys.exit(1)

    results_dir = sys.argv[1]
    fmt = "terminal"
    for i, arg in enumerate(sys.argv):
        if arg == "--format" and i + 1 < len(sys.argv):
            fmt = sys.argv[i + 1]

    results = load_results(results_dir)
    if not results:
        print("No scored results found.", file=sys.stderr)
        sys.exit(1)

    tables = build_per_scenario_table(results)
    aggregate = build_aggregate(results)
    header = {"total": len(results)}

    if fmt == "terminal":
        print(format_terminal(tables, aggregate, header))
    elif fmt == "markdown":
        output = format_markdown(tables, aggregate, header)
        print(output)
        md_path = os.path.join(results_dir, "report.md")
        with open(md_path, "w") as f:
            f.write(output)
        print(f"Written to {md_path}", file=sys.stderr)

        # Hallucination log lives next to report.md and is regenerated
        # on every markdown run so it never drifts from the scored data.
        halluc_output = format_hallucination_log(results)
        halluc_path = os.path.join(results_dir, "citation-hallucinations.md")
        with open(halluc_path, "w") as f:
            f.write(halluc_output)
        print(f"Written to {halluc_path}", file=sys.stderr)
    elif fmt == "json":
        output = format_json(tables, aggregate, header)
        print(output)
        json_path = os.path.join(results_dir, "report.json")
        with open(json_path, "w") as f:
            f.write(output)
        print(f"Written to {json_path}", file=sys.stderr)
