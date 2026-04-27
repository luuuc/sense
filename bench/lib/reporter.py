#!/usr/bin/env python3
"""Aggregate scored results into comparison tables.

Usage: reporter.py <results_dir> [--format terminal|markdown|json]

Reads scored.json files from results/<tool>/<repo>/<task>/.
Produces per-task tables, per-repo summaries, and aggregate summary.
"""

import json
import os
import sys


def load_scored_results(results_dir):
    """Walk results dir and load all scored.json files.

    Also loads run_meta.json for tool versions and repo commits.
    """
    results = []
    for tool in sorted(os.listdir(results_dir)):
        tool_dir = os.path.join(results_dir, tool)
        if not os.path.isdir(tool_dir) or tool.startswith("."):
            continue
        for repo in sorted(os.listdir(tool_dir)):
            repo_dir = os.path.join(tool_dir, repo)
            if not os.path.isdir(repo_dir):
                continue
            for task in sorted(os.listdir(repo_dir)):
                task_dir = os.path.join(repo_dir, task)
                scored_path = os.path.join(task_dir, "scored.json")
                if not os.path.exists(scored_path):
                    continue
                with open(scored_path) as f:
                    result = json.load(f)
                meta_path = os.path.join(task_dir, "run_meta.json")
                if os.path.exists(meta_path):
                    with open(meta_path) as f:
                        meta = json.load(f)
                    result["_tool_version"] = meta.get("tool_version")
                    result["_repo_commit"] = meta.get("repo_commit")
                    result["_timestamp"] = meta.get("timestamp")
                results.append(result)
    return results


def build_header(results):
    """Build a report header with counts, tool versions, and repo commits."""
    tools = sorted(set(r["tool"] for r in results))
    repos = sorted(set(r["repo"] for r in results))
    tasks = sorted(set(r["task"] for r in results))

    tool_versions = {}
    for r in results:
        v = r.get("_tool_version")
        if v and r["tool"] not in tool_versions:
            tool_versions[r["tool"]] = v

    repo_commits = {}
    for r in results:
        c = r.get("_repo_commit")
        if c and r["repo"] not in repo_commits:
            repo_commits[r["repo"]] = c

    timestamps = [r.get("_timestamp") for r in results if r.get("_timestamp")]
    ts_range = ""
    if timestamps:
        ts_range = f"{min(timestamps)} — {max(timestamps)}"

    return {
        "total": len(results),
        "tools": tools,
        "repos": repos,
        "tasks": tasks,
        "tool_versions": tool_versions,
        "repo_commits": repo_commits,
        "timestamp_range": ts_range,
    }


def correctness_score(result):
    """Extract a single numeric score from correctness."""
    c = result.get("correctness", {})
    ctype = c.get("type", "")
    if ctype == "set_match":
        return c.get("f1", 0.0)
    elif ctype == "keyword_presence":
        return c.get("score", 0.0)
    return None


def token_savings(tool_tokens, baseline_tokens):
    """Compute savings relative to baseline."""
    if not baseline_tokens or baseline_tokens == 0:
        return None
    return 1.0 - (tool_tokens / baseline_tokens)


def build_tables(results):
    """Build per-(repo, task) comparison tables."""
    by_repo_task = {}
    for r in results:
        key = (r["repo"], r["task"])
        by_repo_task.setdefault(key, {})[r["tool"]] = r

    tables = []
    for (repo, task), tool_results in sorted(by_repo_task.items()):
        baseline = tool_results.get("baseline", {})
        baseline_tokens = baseline.get("metrics", {}).get("token_total", 0)

        rows = []
        for tool_name in sorted(tool_results.keys()):
            r = tool_results[tool_name]
            m = r.get("metrics", {})
            misses = r.get("misses", {})
            score = correctness_score(r)
            tokens = m.get("token_total", 0)
            savings = token_savings(tokens, baseline_tokens)

            rows.append({
                "tool": tool_name,
                "calls": m.get("tool_calls", 0),
                "misses": misses.get("total", 0) if tool_name != "baseline" else None,
                "tokens": tokens,
                "savings": savings if tool_name != "baseline" else None,
                "score": score,
                "score_type": r.get("correctness", {}).get("type", ""),
                "wall_time": m.get("wall_time_seconds"),
                "cost_usd": m.get("cost_usd"),
                "gt_status": r.get("ground_truth_status", ""),
            })

        tables.append({
            "repo": repo,
            "task": task,
            "rows": rows,
        })

    return tables


def build_aggregate(results):
    """Build per-tool aggregate across all repos and tasks."""
    by_tool = {}
    for r in results:
        tool = r["tool"]
        by_tool.setdefault(tool, []).append(r)

    agg = []
    for tool_name in sorted(by_tool.keys()):
        runs = by_tool[tool_name]
        n = len(runs)
        calls = [r.get("metrics", {}).get("tool_calls", 0) for r in runs]
        miss_totals = [
            r.get("misses", {}).get("total", 0)
            for r in runs
            if r["tool"] != "baseline"
        ]
        tokens = [r.get("metrics", {}).get("token_total", 0) for r in runs]
        scores = [correctness_score(r) for r in runs]
        scores = [s for s in scores if s is not None]
        times = [
            r.get("metrics", {}).get("wall_time_seconds", 0)
            for r in runs
            if r.get("metrics", {}).get("wall_time_seconds") is not None
        ]
        costs = [
            r.get("metrics", {}).get("cost_usd", 0)
            for r in runs
            if r.get("metrics", {}).get("cost_usd") is not None
        ]

        def avg(lst):
            return sum(lst) / len(lst) if lst else 0.0

        agg.append({
            "tool": tool_name,
            "runs": n,
            "avg_calls": round(avg(calls), 1),
            "avg_misses": round(avg(miss_totals), 1) if tool_name != "baseline" else None,
            "avg_tokens": round(avg(tokens)),
            "avg_score": round(avg(scores), 4) if scores else None,
            "avg_time": round(avg(times), 1) if times else None,
            "total_cost": round(sum(costs), 2) if costs else None,
        })

    return agg


def format_terminal(tables, aggregate, header):
    """Produce terminal-friendly comparison tables."""
    lines = []

    lines.append(f"Competitive Evaluation — {header['total']} results, "
                 f"{len(header['tools'])} tools × {len(header['repos'])} repos "
                 f"× {len(header['tasks'])} tasks")

    tool_strs = []
    for t in header["tools"]:
        v = header["tool_versions"].get(t)
        tool_strs.append(f"{t} {v}" if v else t)
    lines.append(f"Tools: {', '.join(tool_strs)}")

    repo_strs = []
    for r in header["repos"]:
        c = header["repo_commits"].get(r)
        repo_strs.append(f"{r}@{c}" if c else r)
    lines.append(f"Repos: {', '.join(repo_strs)}")

    if header["timestamp_range"]:
        lines.append(f"Run:   {header['timestamp_range']}")

    for table in tables:
        lines.append("")
        lines.append(f"{'=' * 72}")
        lines.append(f"  {table['repo']} / {table['task']}")
        lines.append(f"{'=' * 72}")
        lines.append("")
        lines.append(
            f"  {'Tool':<14} {'Calls':>5} {'Misses':>6} {'Tokens':>8} "
            f"{'Savings':>8} {'Score':>6} {'Time':>7}"
        )
        lines.append(f"  {'-' * 66}")

        for row in table["rows"]:
            misses = f"{row['misses']:>6}" if row["misses"] is not None else "     —"
            savings = (
                f"{row['savings']:>7.0%}" if row["savings"] is not None else "      —"
            )
            score = f"{row['score']:>6.2f}" if row["score"] is not None else "     —"
            time_s = (
                f"{row['wall_time']:>6.1f}s"
                if row["wall_time"] is not None
                else "      —"
            )
            lines.append(
                f"  {row['tool']:<14} {row['calls']:>5} {misses} "
                f"{row['tokens']:>8,} {savings} {score} {time_s}"
            )

    lines.append("")
    lines.append(f"{'=' * 72}")
    lines.append("  AGGREGATE (all tasks, all repos)")
    lines.append(f"{'=' * 72}")
    lines.append("")
    lines.append(
        f"  {'Tool':<14} {'Runs':>4} {'Avg Calls':>9} {'Avg Miss':>8} "
        f"{'Avg Tokens':>10} {'Avg Score':>9} {'Avg Time':>8} {'Cost':>7}"
    )
    lines.append(f"  {'-' * 72}")

    for row in aggregate:
        misses = f"{row['avg_misses']:>8.1f}" if row["avg_misses"] is not None else "       —"
        score = f"{row['avg_score']:>9.4f}" if row["avg_score"] is not None else "        —"
        time_s = f"{row['avg_time']:>7.1f}s" if row["avg_time"] is not None else "       —"
        cost = f"${row['total_cost']:>6.2f}" if row["total_cost"] is not None else "      —"
        lines.append(
            f"  {row['tool']:<14} {row['runs']:>4} {row['avg_calls']:>9.1f} "
            f"{misses} {row['avg_tokens']:>10,} {score} {time_s} {cost}"
        )

    lines.append("")
    return "\n".join(lines)


def format_markdown(tables, aggregate, header):
    """Produce markdown comparison tables for README."""
    lines = []

    lines.append("## Competitive Evaluation")
    lines.append("")
    tool_strs = []
    for t in header["tools"]:
        v = header["tool_versions"].get(t)
        tool_strs.append(f"**{t}** {v}" if v else f"**{t}**")
    lines.append(f"Tools: {', '.join(tool_strs)}  ")

    repo_strs = []
    for r in header["repos"]:
        c = header["repo_commits"].get(r)
        repo_strs.append(f"`{r}`@`{c}`" if c else f"`{r}`")
    lines.append(f"Repos: {', '.join(repo_strs)}  ")

    lines.append(f"Results: {header['total']} scored runs  ")
    if header["timestamp_range"]:
        lines.append(f"Run: {header['timestamp_range']}")
    lines.append("")

    for table in tables:
        lines.append(f"### {table['repo']} / {table['task']}")
        lines.append("")
        lines.append("| Tool | Calls | Misses | Tokens | Savings | Score | Time |")
        lines.append("|------|------:|-------:|-------:|--------:|------:|-----:|")

        for row in table["rows"]:
            misses = str(row["misses"]) if row["misses"] is not None else "—"
            savings = (
                f"{row['savings']:.0%}" if row["savings"] is not None else "—"
            )
            score = f"{row['score']:.2f}" if row["score"] is not None else "—"
            time_s = (
                f"{row['wall_time']:.1f}s"
                if row["wall_time"] is not None
                else "—"
            )
            lines.append(
                f"| {row['tool']} | {row['calls']} | {misses} | "
                f"{row['tokens']:,} | {savings} | {score} | {time_s} |"
            )
        lines.append("")

    lines.append("### Aggregate")
    lines.append("")
    lines.append(
        "| Tool | Runs | Avg Calls | Avg Misses | Avg Tokens | "
        "Avg Score | Avg Time | Cost |"
    )
    lines.append("|------|-----:|----------:|-----------:|-----------:|"
                 "---------:|---------:|-----:|")

    for row in aggregate:
        misses = f"{row['avg_misses']:.1f}" if row["avg_misses"] is not None else "—"
        score = f"{row['avg_score']:.4f}" if row["avg_score"] is not None else "—"
        time_s = f"{row['avg_time']:.1f}s" if row["avg_time"] is not None else "—"
        cost = f"${row['total_cost']:.2f}" if row["total_cost"] is not None else "—"
        lines.append(
            f"| {row['tool']} | {row['runs']} | {row['avg_calls']:.1f} | "
            f"{misses} | {row['avg_tokens']:,} | {score} | {time_s} | {cost} |"
        )

    lines.append("")
    return "\n".join(lines)


def format_json(tables, aggregate, header):
    """Produce machine-readable JSON report."""
    return json.dumps({"header": header, "tables": tables, "aggregate": aggregate}, indent=2)


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: reporter.py <results_dir> [--format terminal|markdown|json]",
              file=sys.stderr)
        sys.exit(1)

    results_dir = sys.argv[1]
    fmt = "terminal"
    for i, arg in enumerate(sys.argv):
        if arg == "--format" and i + 1 < len(sys.argv):
            fmt = sys.argv[i + 1]

    results = load_scored_results(results_dir)
    if not results:
        print("No scored results found.", file=sys.stderr)
        sys.exit(1)

    header = build_header(results)
    tables = build_tables(results)
    aggregate = build_aggregate(results)

    if fmt == "terminal":
        print(format_terminal(tables, aggregate, header))
    elif fmt == "markdown":
        print(format_markdown(tables, aggregate, header))
    elif fmt == "json":
        print(format_json(tables, aggregate, header))
    else:
        print(f"Unknown format: {fmt}", file=sys.stderr)
        sys.exit(1)
