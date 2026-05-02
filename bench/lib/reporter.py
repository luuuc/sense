#!/usr/bin/env python3
"""Aggregate scored results into comparison tables.

Usage: reporter.py <results_dir> [--format terminal|markdown|json]

Reads scored.json files from results/<tool>/<repo>/<task>/.
Produces per-task tables, per-repo summaries, and aggregate summary.
"""

import json
import os
import sys


TASK_DESCRIPTIONS = {
    "callers": "Find all callers of a specific symbol. Tests structural code navigation — tools with a call graph should excel. Scored by F1 against a verified caller list.",
    "blast-radius": "Determine what breaks if a symbol changes. Tests impact analysis — requires understanding transitive dependencies. Scored by F1 against known affected symbols.",
    "dead-code": "Find unused symbols in a codebase area. Tests reachability analysis — the tool must prove no callers exist. Scored by F1 against verified dead symbols.",
    "semantic-search": "Find code related to a concept using natural language. Tests semantic understanding beyond keyword matching. Scored by F1 against relevant results.",
    "conventions": "Identify patterns and conventions in a code domain. Tests architectural understanding — the tool must recognize recurring patterns. Scored by keyword presence (qualitative).",
    "orient": "Orient in an unfamiliar codebase — identify architecture and key abstractions. Tests high-level understanding without a specific target. Scored by keyword presence (qualitative).",
    "refactor": "Assess what to know before refactoring a symbol. Tests the ability to surface dependencies, callers, and risks. Scored by keyword presence (qualitative).",
    "grep-task": "Find all locations matching a specific code pattern (exact text search). Tests raw search — where grep excels over semantic tools. Scored by F1 against grep ground truth.",
    "test-file": "Find the test file for a given source file. Tests file-mapping knowledge — convention-aware navigation. Scored by F1 against known test files.",
    "data-flow": "Trace data flow from an entry point to storage. Tests deep architectural tracing across layers. Scored by F1 against a verified call chain.",
}

METRIC_DIRECTIONS = {
    "score": ("higher", "Correctness score — higher is better"),
    "tokens": ("lower", "Total tokens consumed — lower is better (cheaper)"),
    "savings": ("higher", "Token savings vs. baseline — higher is better"),
    "calls": ("lower", "Number of tool calls — lower means more efficient"),
    "misses": ("lower", "MCP tool bypasses (used grep/Read instead) — lower is better"),
    "wall_time": ("lower", "Wall-clock time — lower is better"),
    "cost_usd": ("lower", "API cost in USD — lower is better"),
}


def load_scored_results(results_dir):
    """Walk results dir and load all scored.json files.

    Also loads run_meta.json for tool versions and repo commits,
    and index_meta_setup.json for scan/indexing timing.
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
                if not os.path.isdir(task_dir):
                    continue

                def _load_from(d):
                    scored_path = os.path.join(d, "scored.json")
                    if not os.path.exists(scored_path):
                        return
                    with open(scored_path) as f:
                        result = json.load(f)
                    meta_path = os.path.join(d, "run_meta.json")
                    if os.path.exists(meta_path):
                        with open(meta_path) as f:
                            meta = json.load(f)
                        result["_tool_version"] = meta.get("tool_version")
                        result["_repo_commit"] = meta.get("repo_commit")
                        result["_timestamp"] = meta.get("timestamp")
                    setup_path = os.path.join(d, "index_meta_setup.json")
                    if os.path.exists(setup_path):
                        with open(setup_path) as f:
                            setup_meta = json.load(f)
                        result["_setup_time"] = setup_meta.get("setup_time_seconds")
                        result["_includes_embeddings"] = setup_meta.get("includes_embeddings")
                        result["_deferred_embeddings"] = setup_meta.get("deferred_embeddings")
                    results.append(result)

                _load_from(task_dir)
                for entry in sorted(os.listdir(task_dir)):
                    if entry.startswith("run-"):
                        run_dir = os.path.join(task_dir, entry)
                        if os.path.isdir(run_dir):
                            _load_from(run_dir)
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

        rows.sort(key=lambda r: (r["score"] or 0), reverse=True)

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
        scores_verified = [
            correctness_score(r) for r in runs
            if r.get("ground_truth_status") == "verified"
            and correctness_score(r) is not None
        ]
        scores_initial = [
            correctness_score(r) for r in runs
            if r.get("ground_truth_status") == "initial"
            and correctness_score(r) is not None
        ]
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
            "avg_score_verified": round(avg(scores_verified), 4) if scores_verified else None,
            "avg_score_initial": round(avg(scores_initial), 4) if scores_initial else None,
            "avg_time": round(avg(times), 1) if times else None,
            "total_cost": round(sum(costs), 2) if costs else None,
        })

    return agg


def build_scan_times(results):
    """Build per-tool scan/index timing table."""
    seen = {}
    for r in results:
        tool = r["tool"]
        repo = r["repo"]
        key = (tool, repo)
        if key in seen:
            continue
        st = r.get("_setup_time")
        if st is not None:
            seen[key] = {
                "tool": tool,
                "repo": repo,
                "setup_time": st,
                "includes_embeddings": r.get("_includes_embeddings", False),
                "deferred_embeddings": r.get("_deferred_embeddings", False),
            }

    by_tool = {}
    for entry in seen.values():
        by_tool.setdefault(entry["tool"], []).append(entry)

    rows = []
    for tool in sorted(by_tool.keys()):
        entries = by_tool[tool]
        times = [e["setup_time"] for e in entries]
        deferred = any(e["deferred_embeddings"] for e in entries)
        includes_emb = all(e["includes_embeddings"] for e in entries)
        avg_time = sum(times) / len(times) if times else 0
        rows.append({
            "tool": tool,
            "repos_measured": len(entries),
            "avg_scan_seconds": round(avg_time, 1),
            "min_scan_seconds": min(times) if times else None,
            "max_scan_seconds": max(times) if times else None,
            "includes_embeddings": includes_emb,
            "deferred_embeddings": deferred,
            "per_repo": {e["repo"]: e["setup_time"] for e in entries},
        })
    rows.sort(key=lambda r: r["avg_scan_seconds"])
    return rows


def build_per_task_rankings(results):
    """Build per-task tool rankings by average score."""
    by_task_tool = {}
    for r in results:
        key = (r["task"], r["tool"])
        by_task_tool.setdefault(key, []).append(r)

    by_task = {}
    for (task, tool), runs in by_task_tool.items():
        scores = [correctness_score(r) for r in runs if correctness_score(r) is not None]
        tokens = [r.get("metrics", {}).get("token_total", 0) for r in runs]
        times = [r.get("metrics", {}).get("wall_time_seconds", 0) for r in runs
                 if r.get("metrics", {}).get("wall_time_seconds") is not None]
        avg_score = sum(scores) / len(scores) if scores else None
        avg_tokens = sum(tokens) / len(tokens) if tokens else 0
        avg_time = sum(times) / len(times) if times else None
        by_task.setdefault(task, []).append({
            "tool": tool,
            "avg_score": round(avg_score, 4) if avg_score is not None else None,
            "avg_tokens": round(avg_tokens),
            "avg_time": round(avg_time, 1) if avg_time is not None else None,
            "runs": len(runs),
        })

    for task in by_task:
        by_task[task].sort(key=lambda r: (r["avg_score"] or -1), reverse=True)

    return by_task


def build_token_savings_table(results):
    """Build per-tool token savings vs baseline."""
    by_repo_task = {}
    for r in results:
        key = (r["repo"], r["task"])
        by_repo_task.setdefault(key, {})[r["tool"]] = r

    tool_savings = {}
    for (repo, task), tool_results in by_repo_task.items():
        baseline = tool_results.get("baseline", {})
        baseline_tokens = baseline.get("metrics", {}).get("token_total", 0)
        if not baseline_tokens:
            continue
        for tool_name, r in tool_results.items():
            if tool_name == "baseline":
                continue
            tool_tokens = r.get("metrics", {}).get("token_total", 0)
            savings = token_savings(tool_tokens, baseline_tokens)
            if savings is not None:
                tool_savings.setdefault(tool_name, []).append(savings)

    rows = []
    for tool in sorted(tool_savings.keys()):
        vals = tool_savings[tool]
        avg_sav = sum(vals) / len(vals) if vals else 0
        positive = sum(1 for v in vals if v > 0)
        rows.append({
            "tool": tool,
            "avg_savings": round(avg_sav, 4),
            "tasks_saved": positive,
            "tasks_total": len(vals),
            "best_savings": round(max(vals), 4) if vals else 0,
            "worst_savings": round(min(vals), 4) if vals else 0,
        })
    rows.sort(key=lambda r: r["avg_savings"], reverse=True)
    return rows


def build_efficiency_table(results):
    """Build efficiency ranking: score per 100k tokens."""
    by_tool = {}
    for r in results:
        tool = r["tool"]
        score = correctness_score(r)
        tokens = r.get("metrics", {}).get("token_total", 0)
        time_s = r.get("metrics", {}).get("wall_time_seconds")
        if score is not None:
            by_tool.setdefault(tool, {"scores": [], "tokens": [], "times": []})
            by_tool[tool]["scores"].append(score)
            by_tool[tool]["tokens"].append(tokens)
            if time_s is not None:
                by_tool[tool]["times"].append(time_s)

    rows = []
    for tool in sorted(by_tool.keys()):
        d = by_tool[tool]
        avg_score = sum(d["scores"]) / len(d["scores"]) if d["scores"] else 0
        avg_tokens = sum(d["tokens"]) / len(d["tokens"]) if d["tokens"] else 0
        avg_time = sum(d["times"]) / len(d["times"]) if d["times"] else None
        score_per_100k = (avg_score / (avg_tokens / 100000)) if avg_tokens > 0 else 0
        score_per_minute = (avg_score / (avg_time / 60)) if avg_time and avg_time > 0 else None
        rows.append({
            "tool": tool,
            "avg_score": round(avg_score, 4),
            "avg_tokens": round(avg_tokens),
            "score_per_100k": round(score_per_100k, 4),
            "avg_time": round(avg_time, 1) if avg_time else None,
            "score_per_minute": round(score_per_minute, 4) if score_per_minute else None,
        })
    rows.sort(key=lambda r: r["score_per_100k"], reverse=True)
    return rows


def build_global_ranking(results):
    """Build global tool ranking by composite score."""
    by_tool = {}
    for r in results:
        tool = r["tool"]
        by_tool.setdefault(tool, {"scores": [], "tokens": [], "times": [], "costs": []})
        score = correctness_score(r)
        if score is not None:
            by_tool[tool]["scores"].append(score)
        by_tool[tool]["tokens"].append(r.get("metrics", {}).get("token_total", 0))
        t = r.get("metrics", {}).get("wall_time_seconds")
        if t is not None:
            by_tool[tool]["times"].append(t)
        c = r.get("metrics", {}).get("cost_usd")
        if c is not None:
            by_tool[tool]["costs"].append(c)

    rows = []
    for tool in sorted(by_tool.keys()):
        d = by_tool[tool]
        avg_score = sum(d["scores"]) / len(d["scores"]) if d["scores"] else 0
        avg_tokens = sum(d["tokens"]) / len(d["tokens"]) if d["tokens"] else 0
        avg_time = sum(d["times"]) / len(d["times"]) if d["times"] else None
        total_cost = sum(d["costs"]) if d["costs"] else None
        rows.append({
            "tool": tool,
            "avg_score": round(avg_score, 4),
            "avg_tokens": round(avg_tokens),
            "avg_time": round(avg_time, 1) if avg_time else None,
            "total_cost": round(total_cost, 2) if total_cost is not None else None,
            "runs": len(d["tokens"]),
        })
    rows.sort(key=lambda r: r["avg_score"], reverse=True)
    return rows


def format_terminal(tables, aggregate, header, **kwargs):
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
        f"{'Avg Tokens':>10} {'Avg Score':>9} {'Verified':>9} {'Initial':>9} "
        f"{'Avg Time':>8} {'Cost':>7}"
    )
    lines.append(f"  {'-' * 90}")

    for row in aggregate:
        misses = f"{row['avg_misses']:>8.1f}" if row["avg_misses"] is not None else "       —"
        score = f"{row['avg_score']:>9.4f}" if row["avg_score"] is not None else "        —"
        v_score = f"{row['avg_score_verified']:>9.4f}" if row["avg_score_verified"] is not None else "        —"
        i_score = f"{row['avg_score_initial']:>9.4f}" if row["avg_score_initial"] is not None else "        —"
        time_s = f"{row['avg_time']:>7.1f}s" if row["avg_time"] is not None else "       —"
        cost = f"${row['total_cost']:>6.2f}" if row["total_cost"] is not None else "      —"
        lines.append(
            f"  {row['tool']:<14} {row['runs']:>4} {row['avg_calls']:>9.1f} "
            f"{misses} {row['avg_tokens']:>10,} {score} {v_score} {i_score} "
            f"{time_s} {cost}"
        )

    lines.append("")
    return "\n".join(lines)


def _rank_badge(i):
    if i == 0:
        return " :1st_place_medal:"
    if i == 1:
        return " :2nd_place_medal:"
    if i == 2:
        return " :3rd_place_medal:"
    return ""


def format_markdown(tables, aggregate, header, **kwargs):
    """Produce markdown comparison tables for README."""
    scan_times = kwargs.get("scan_times", [])
    per_task = kwargs.get("per_task_rankings", {})
    token_savings_table = kwargs.get("token_savings_table", [])
    efficiency = kwargs.get("efficiency", [])
    global_ranking = kwargs.get("global_ranking", [])

    lines = []

    # --- Header ---
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

    # --- Metric legend ---
    lines.append("### Reading the scores")
    lines.append("")
    lines.append("| Metric | Best | Meaning |")
    lines.append("|--------|------|---------|")
    for metric, (direction, meaning) in METRIC_DIRECTIONS.items():
        arrow = "Higher" if direction == "higher" else "Lower"
        lines.append(f"| {metric} | {arrow} | {meaning} |")
    lines.append("")

    # --- Initial scan / indexing time ---
    if scan_times:
        lines.append("### Initial scan time")
        lines.append("")
        lines.append("Time to parse and index the codebase before the first query. Measured once per tool per repo.")
        lines.append("")
        lines.append("| Tool | Avg (s) | Min (s) | Max (s) | Includes embeddings | Deferred embeddings |")
        lines.append("|------|--------:|--------:|--------:|:-------------------:|:-------------------:|")
        for row in scan_times:
            incl = "Yes" if row["includes_embeddings"] else "No"
            deferred = "Yes" if row["deferred_embeddings"] else "No"
            min_s = f"{row['min_scan_seconds']}" if row["min_scan_seconds"] is not None else "—"
            max_s = f"{row['max_scan_seconds']}" if row["max_scan_seconds"] is not None else "—"
            lines.append(
                f"| {row['tool']} | {row['avg_scan_seconds']:.1f} | "
                f"{min_s} | {max_s} | {incl} | {deferred} |"
            )
        lines.append("")
        lines.append("> **Deferred embeddings**: when \"Yes\", the tool returns from `init` before embeddings are complete — ")
        lines.append("> it continues indexing in a background process. The scan time shown excludes that background work.")
        lines.append("")

    # --- Per-task results (ranked by score) ---
    for table in tables:
        task = table["task"]
        desc = TASK_DESCRIPTIONS.get(task, "")
        lines.append(f"### {table['repo']} / {table['task']}")
        lines.append("")
        if desc:
            lines.append(f"> {desc}")
            lines.append("")
        lines.append("| Rank | Tool | Calls | Misses | Tokens | Savings | Score | Time |")
        lines.append("|-----:|------|------:|-------:|-------:|--------:|------:|-----:|")

        for i, row in enumerate(table["rows"]):
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
            badge = _rank_badge(i)
            lines.append(
                f"| {i+1} | {row['tool']}{badge} | {row['calls']} | {misses} | "
                f"{row['tokens']:,} | {savings} | {score} | {time_s} |"
            )
        lines.append("")

    # --- Token savings section ---
    if token_savings_table:
        lines.append("### Token savings vs. baseline")
        lines.append("")
        lines.append("How much each tool saves compared to Claude with no MCP tools (baseline). "
                      "Positive means fewer tokens; negative means more tokens than baseline.")
        lines.append("")
        lines.append("| Rank | Tool | Avg savings | Best | Worst | Tasks with savings | Total tasks |")
        lines.append("|-----:|------|------------:|-----:|------:|-------------------:|------------:|")
        for i, row in enumerate(token_savings_table):
            badge = _rank_badge(i)
            lines.append(
                f"| {i+1} | {row['tool']}{badge} | {row['avg_savings']:.0%} | "
                f"{row['best_savings']:.0%} | {row['worst_savings']:.0%} | "
                f"{row['tasks_saved']} | {row['tasks_total']} |"
            )
        lines.append("")

    # --- Efficiency section ---
    if efficiency:
        lines.append("### Efficiency")
        lines.append("")
        lines.append("Score per 100k tokens measures how much correctness each tool extracts per unit of "
                      "token spend. Score per minute measures throughput. Higher is better for both.")
        lines.append("")
        lines.append("| Rank | Tool | Avg score | Avg tokens | Score/100k tokens | Avg time | Score/minute |")
        lines.append("|-----:|------|----------:|-----------:|------------------:|---------:|-------------:|")
        for i, row in enumerate(efficiency):
            badge = _rank_badge(i)
            time_s = f"{row['avg_time']:.1f}s" if row["avg_time"] is not None else "—"
            spm = f"{row['score_per_minute']:.4f}" if row["score_per_minute"] is not None else "—"
            lines.append(
                f"| {i+1} | {row['tool']}{badge} | {row['avg_score']:.4f} | "
                f"{row['avg_tokens']:,} | {row['score_per_100k']:.4f} | "
                f"{time_s} | {spm} |"
            )
        lines.append("")

    # --- Per-task best tool ---
    if per_task:
        lines.append("### Best tool per task")
        lines.append("")
        lines.append("Tools ranked by average score for each task across all repos.")
        lines.append("")
        for task in sorted(per_task.keys()):
            desc = TASK_DESCRIPTIONS.get(task, "")
            task_rows = per_task[task]
            lines.append(f"#### {task}")
            if desc:
                lines.append(f"> {desc}")
            lines.append("")
            lines.append("| Rank | Tool | Avg score | Avg tokens | Avg time | Runs |")
            lines.append("|-----:|------|----------:|-----------:|---------:|-----:|")
            for i, row in enumerate(task_rows):
                badge = _rank_badge(i)
                score = f"{row['avg_score']:.4f}" if row["avg_score"] is not None else "—"
                time_s = f"{row['avg_time']:.1f}s" if row["avg_time"] is not None else "—"
                lines.append(
                    f"| {i+1} | {row['tool']}{badge} | {score} | "
                    f"{row['avg_tokens']:,} | {time_s} | {row['runs']} |"
                )
            lines.append("")

    # --- Aggregate ---
    lines.append("### Aggregate")
    lines.append("")
    lines.append(
        "| Tool | Runs | Avg Calls | Avg Misses | Avg Tokens | "
        "Avg Score | Verified Score | Initial Score | Avg Time | Cost |"
    )
    lines.append("|------|-----:|----------:|-----------:|-----------:|"
                 "---------:|---------------:|--------------:|---------:|-----:|")

    for row in aggregate:
        misses = f"{row['avg_misses']:.1f}" if row["avg_misses"] is not None else "—"
        score = f"{row['avg_score']:.4f}" if row["avg_score"] is not None else "—"
        v_score = f"{row['avg_score_verified']:.4f}" if row["avg_score_verified"] is not None else "—"
        i_score = f"{row['avg_score_initial']:.4f}" if row["avg_score_initial"] is not None else "—"
        time_s = f"{row['avg_time']:.1f}s" if row["avg_time"] is not None else "—"
        cost = f"${row['total_cost']:.2f}" if row["total_cost"] is not None else "—"
        lines.append(
            f"| {row['tool']} | {row['runs']} | {row['avg_calls']:.1f} | "
            f"{misses} | {row['avg_tokens']:,} | {score} | {v_score} | {i_score} | {time_s} | {cost} |"
        )

    lines.append("")

    # --- Global ranking ---
    if global_ranking:
        lines.append("### Global ranking")
        lines.append("")
        lines.append("Tools ranked by average correctness score across all tasks and repos.")
        lines.append("")
        lines.append("| Rank | Tool | Avg score | Avg tokens | Avg time | Total cost | Runs |")
        lines.append("|-----:|------|----------:|-----------:|---------:|-----------:|-----:|")
        for i, row in enumerate(global_ranking):
            badge = _rank_badge(i)
            time_s = f"{row['avg_time']:.1f}s" if row["avg_time"] is not None else "—"
            cost = f"${row['total_cost']:.2f}" if row["total_cost"] is not None else "—"
            lines.append(
                f"| {i+1} | {row['tool']}{badge} | {row['avg_score']:.4f} | "
                f"{row['avg_tokens']:,} | {time_s} | {cost} | {row['runs']} |"
            )
        lines.append("")

    return "\n".join(lines)


def format_json(tables, aggregate, header, **kwargs):
    """Produce machine-readable JSON report."""
    data = {"header": header, "tables": tables, "aggregate": aggregate}
    for key in ("scan_times", "per_task_rankings", "token_savings_table",
                "efficiency", "global_ranking"):
        if key in kwargs:
            data[key] = kwargs[key]
    return json.dumps(data, indent=2)


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
    scan_times = build_scan_times(results)
    per_task_rankings = build_per_task_rankings(results)
    token_savings_table = build_token_savings_table(results)
    efficiency = build_efficiency_table(results)
    global_ranking = build_global_ranking(results)

    extra = dict(
        scan_times=scan_times,
        per_task_rankings=per_task_rankings,
        token_savings_table=token_savings_table,
        efficiency=efficiency,
        global_ranking=global_ranking,
    )

    if fmt == "terminal":
        print(format_terminal(tables, aggregate, header, **extra))
    elif fmt == "markdown":
        print(format_markdown(tables, aggregate, header, **extra))
    elif fmt == "json":
        print(format_json(tables, aggregate, header, **extra))
    else:
        print(f"Unknown format: {fmt}", file=sys.stderr)
        sys.exit(1)
