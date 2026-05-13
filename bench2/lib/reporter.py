#!/usr/bin/env python3
"""Aggregate scenario benchmark results into comparison tables.

Usage: reporter.py <results_dir> [--format terminal|markdown|json]

Reads scored.json files from results/<tool>/<repo>/ (one per scenario).
Produces per-scenario comparison tables and an aggregate ranking.
"""

import json
import os
import sys


SCENARIO_DESCRIPTIONS = {
    "flask": "Multi-step Flask refactoring: trace WSGI dispatch, locate tests, add a debug parameter, verify the change. Tests call graph traversal, test-file mapping, and safe code modification awareness.",
    "gin": "Multi-step Gin exploration: understand middleware chaining, trace HTTP dispatch, find dead code, modify the recovery middleware. Tests data flow tracing, dead code detection, and structural editing awareness.",
    "axum": "Multi-step Axum refactoring: trace Handler trait propagation, understand extractor chaining, add a request ID layer. Tests Rust trait analysis, Tower middleware comprehension, and layered modification.",
    "discourse": "Multi-step Discourse exploration: trace topic creation flow from controller to persistence, locate specs, understand Guardian authorization. Tests Rails service object tracing and test convention awareness.",
    "javalin": "Multi-step Javalin exploration: understand servlet dispatch, trace routing table construction, add a custom error handler. Tests Java framework comprehension and handler registration patterns.",
    "nextjs": "Multi-step Next.js exploration: trace SSR render path, understand route matching, thread a request ID. Tests TypeScript monorepo navigation and complex server-side pipeline understanding.",
}


METRIC_DIRECTIONS = {
    "fairness": ("higher", "Fairness score — correctness (0.70) + efficiency (0.30), excludes tool-adoption checks"),
    "adoption": ("higher", "Adoption score — tool fluency + discoverability, for code-intel comparisons only"),
    "correctness": ("higher", "Checklist hit rate excluding adoption-layer checks"),
    "efficiency": ("higher", "Half token efficiency + half time efficiency, each calibrated per repo"),
    "tokens": ("lower", "Billed tokens (uncached) — lower is better (cheaper)"),
    "wall_time": ("lower", "Wall-clock time — lower is better, folded into efficiency"),
    "cost_usd": ("lower", "API cost in USD — lower is better"),
}


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

                meta_path = os.path.join(repo_dir, "run_meta.json")
                if os.path.exists(meta_path):
                    with open(meta_path) as f:
                        meta = json.load(f)
                    result["_tool_version"] = meta.get("tool_version")
                    result["_repo_commit"] = meta.get("repo_commit")
                    result["_timestamp"] = meta.get("timestamp")

                for run_entry in sorted(os.listdir(repo_dir)):
                    if run_entry.startswith("run-"):
                        run_scored = os.path.join(repo_dir, run_entry, "scored.json")
                        if os.path.exists(run_scored):
                            with open(run_scored) as f:
                                r2 = json.load(f)
                            r2["tool"] = tool
                            r2["repo_key"] = repo
                            r2["_run"] = run_entry
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

            rows.append({
                "tool": tool,
                "fairness_score": r.get("fairness_score"),
                "adoption_score": r.get("adoption_score"),
                "correctness": r.get("correctness"),
                "efficiency": r.get("efficiency"),
                "tokens": m.get("token_total_billed", m.get("token_total", 0)),
                "wall_time": m.get("wall_time_seconds"),
                "cost_usd": m.get("cost_usd"),
                "cost_estimated": bool(m.get("cost_estimated")),
                "failed": bool(r.get("failed")),
                "failure_reason": r.get("failure_reason"),
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
        fairness = [r2.get("fairness_score") for r2 in runs if r2.get("fairness_score") is not None]
        adoption = [r2.get("adoption_score") for r2 in runs if r2.get("adoption_score") is not None]
        correct = [r2.get("correctness") for r2 in runs if r2.get("correctness") is not None]
        effs = [r2.get("efficiency") for r2 in runs if r2.get("efficiency") is not None]
        tokens = [r2.get("metrics", {}).get("token_total_billed", r2.get("metrics", {}).get("token_total", 0)) for r2 in runs]
        times = [r2.get("metrics", {}).get("wall_time_seconds") for r2 in runs
                 if r2.get("metrics", {}).get("wall_time_seconds") is not None]
        costs = [r2.get("metrics", {}).get("cost_usd") for r2 in runs
                 if r2.get("metrics", {}).get("cost_usd") is not None]

        def avg(lst):
            return sum(lst) / len(lst) if lst else 0.0

        failures = sum(1 for r2 in runs if r2.get("failed"))
        agg.append({
            "tool": tool_name,
            "scenarios": n,
            "failures": failures,
            "avg_fairness": round(avg(fairness), 4) if fairness else None,
            "avg_adoption": round(avg(adoption), 4) if adoption else None,
            "avg_correctness": round(avg(correct), 4) if correct else None,
            "avg_efficiency": round(avg(effs), 4) if effs else None,
            "avg_tokens": round(avg(tokens)),
            "avg_time": round(avg(times), 1) if times else None,
            "total_cost": round(sum(costs), 2) if costs else None,
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
        hdr = f"  {'Tool':<14} {'Fair':>6} {'Adopt':>6} {'Corr':>6} {'Eff':>5} {'Tokens':>8} {'Time':>7} {'Cost':>7}"
        lines.append(hdr)
        lines.append(f"  {'-' * 68}")

        for row in table["rows"]:
            if row.get("failed"):
                wt = f"{row['wall_time']:>6.1f}s" if row["wall_time"] else "      —"
                reason = row.get("failure_reason") or "failed"
                lines.append(
                    f"  {row['tool']:<14} FAILED                                    {wt}   ({reason})"
                )
                continue
            fa = f"{row['fairness_score']:>6.3f}" if row["fairness_score"] is not None else "     —"
            ad = f"{row['adoption_score']:>6.3f}" if row["adoption_score"] is not None else "     —"
            cr = f"{row['correctness']:>6.0%}" if row["correctness"] is not None else "     —"
            ef = f"{row['efficiency']:>5.2f}" if row["efficiency"] is not None else "    —"
            tk = f"{row['tokens']:>8,}"
            wt = f"{row['wall_time']:>6.1f}s" if row["wall_time"] else "      —"
            co = f"${row['cost_usd']:>6.2f}" if row["cost_usd"] is not None else "      —"
            lines.append(
                f"  {row['tool']:<14} {fa}  {ad} {cr} {ef} {tk} {wt} {co}"
            )
        lines.append("")

    lines.append(f"{'=' * 78}")
    lines.append("  AGGREGATE (all scenarios)")
    lines.append(f"{'=' * 78}")
    lines.append("")
    lines.append(f"  {'Tool':<14} {'Runs':>4} {'Fail':>4} {'Avg Fair':>8} {'Avg Adopt':>9} {'Avg Corr':>8} {'Avg Eff':>7} {'Avg Tokens':>10} {'Avg Time':>9} {'Cost':>7}")
    lines.append(f"  {'-' * 86}")

    for row in aggregate:
        af = f"{row['avg_fairness']:>8.4f}" if row["avg_fairness"] is not None else "       —"
        aa = f"{row['avg_adoption']:>9.4f}" if row["avg_adoption"] is not None else "        —"
        ac = f"{row['avg_correctness']:>8.1%}" if row["avg_correctness"] is not None else "       —"
        ae = f"{row['avg_efficiency']:>7.4f}" if row["avg_efficiency"] is not None else "      —"
        at = f"{row['avg_time']:>8.1f}s" if row.get("avg_time") is not None else "        —"
        co = f"${row['total_cost']:>6.2f}" if row["total_cost"] else "      —"
        lines.append(
            f"  {row['tool']:<14} {row['scenarios']:>4} {row.get('failures', 0):>4} {af} {aa} {ac} {ae} {row['avg_tokens']:>10,} {at} {co}"
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
    lines.append("Two-layer scoring: **Fairness** (correctness 70% + efficiency 30%) measures answer quality without tool-adoption bias. Efficiency is half token efficiency + half time efficiency, so a slow session is penalized even if it uses few tokens. **Adoption** (tool fluency + discoverability) is for code-intel-vs-code-intel comparisons only.")
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
        lines.append("| Rank | Tool | Fairness | Adoption | Correctness | Efficiency | Tokens | Time | Cost |")
        lines.append("|-----:|------|--------:|---------:|----------:|---------:|-------:|-----:|-----:|")

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
                    f"| {i+1} | {row['tool']} | **FAILED** | — | — | — | — | {wt} | {co} |"
                    f" <!-- {reason} -->"
                )
                continue
            fa = f"{row['fairness_score']:.3f}" if row["fairness_score"] is not None else "—"
            ad = f"{row['adoption_score']:.3f}" if row["adoption_score"] is not None else "—"
            cr = f"{row['correctness']:.0%}" if row["correctness"] is not None else "—"
            ef = f"{row['efficiency']:.2f}" if row["efficiency"] is not None else "—"
            tk = f"{row['tokens']:,}"
            wt = f"{row['wall_time']:.1f}s" if row["wall_time"] else "—"
            co = f"${row['cost_usd']:.2f}" if row["cost_usd"] is not None else "—"
            lines.append(
                f"| {i+1} | {row['tool']}{badge} | {fa} | {ad} | {cr} | {ef} | {tk} | {wt} | {co} |"
            )
        lines.append("")

    lines.append("### Aggregate")
    lines.append("")
    lines.append("Failed runs count as fairness 0 in the average. The `Failures` column shows how many scenarios the tool could not complete. Costs marked with `*` are estimated from per-message token usage in the partial transcript, because the session never emitted a final cost event.")
    lines.append("")
    lines.append("| Rank | Tool | Scenarios | Failures | Avg Fairness | Avg Adoption | Avg Correctness | Avg Efficiency | Avg Tokens | Avg Time | Total Cost |")
    lines.append("|-----:|------|----------:|--------:|------------:|-----------:|---------------:|--------------:|-----------:|--------:|-----------:|")
    for i, row in enumerate(aggregate):
        badge = _rank_badge(i)
        af = f"{row['avg_fairness']:.4f}" if row["avg_fairness"] is not None else "—"
        aa = f"{row['avg_adoption']:.4f}" if row["avg_adoption"] is not None else "—"
        ac = f"{row['avg_correctness']:.4f}" if row["avg_correctness"] is not None else "—"
        ae = f"{row['avg_efficiency']:.4f}" if row.get("avg_efficiency") is not None else "—"
        at = f"{row['avg_time']:.1f}s" if row.get("avg_time") is not None else "—"
        co = f"${row['total_cost']:.2f}" if row["total_cost"] else "—"
        fails = row.get("failures", 0)
        fail_cell = f"**{fails}**" if fails else "0"
        lines.append(
            f"| {i+1} | {row['tool']}{badge} | {row['scenarios']} | {fail_cell} | {af} | {aa} | {ac} | {ae} |"
            f" {row['avg_tokens']:,} | {at} | {co} |"
        )
    lines.append("")

    return "\n".join(lines)


def format_json(tables, aggregate, header):
    return json.dumps({"tables": tables, "aggregate": aggregate}, indent=2)


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
    elif fmt == "json":
        output = format_json(tables, aggregate, header)
        print(output)
        json_path = os.path.join(results_dir, "report.json")
        with open(json_path, "w") as f:
            f.write(output)
        print(f"Written to {json_path}", file=sys.stderr)
