#!/usr/bin/env python3
"""Analyze bench2 transcripts to extract quality patterns and check differentiation.

Usage: analyze-transcripts.py [--results-dir DIR] [--scenarios-dir DIR] [--output PATH]

Reads scored.json and transcript.json files from results/, computes per-check
differentiation between tools, extracts tool usage patterns and quality signals.
Outputs analysis JSON for the improvement loop.
"""

import argparse
import json
import os
import re
import sys


BENCH2_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
sys.path.insert(0, os.path.join(BENCH2_DIR, "lib"))

from scorer import read_transcript_texts, _SOURCE_FILE_RE


def load_scored_results(results_dir, tool, repo):
    base = os.path.join(results_dir, tool, repo)
    runs = []
    if not os.path.isdir(base):
        return runs
    for entry in sorted(os.listdir(base)):
        if entry.startswith("run-"):
            scored = os.path.join(base, entry, "scored.json")
            if os.path.exists(scored):
                with open(scored) as f:
                    runs.append(json.load(f))
    if not runs:
        scored = os.path.join(base, "scored.json")
        if os.path.exists(scored):
            with open(scored) as f:
                runs.append(json.load(f))
    return runs


def discover_repos(results_dir):
    repos = set()
    for tool_dir in os.listdir(results_dir):
        td = os.path.join(results_dir, tool_dir)
        if not os.path.isdir(td) or tool_dir.startswith("."):
            continue
        for repo in os.listdir(td):
            if os.path.isdir(os.path.join(td, repo)) and not repo.startswith("."):
                repos.add(repo)
    return sorted(repos)


def discover_tools(results_dir):
    tools = []
    for d in sorted(os.listdir(results_dir)):
        td = os.path.join(results_dir, d)
        if not os.path.isdir(td) or d.startswith("."):
            continue
        has_scored = any(
            os.path.exists(os.path.join(td, repo, "scored.json"))
            for repo in os.listdir(td)
            if os.path.isdir(os.path.join(td, repo))
        )
        if has_scored:
            tools.append(d)
    return tools


def check_differentiation(sense_runs, baseline_runs, step_idx, check_idx):
    sense_hits = []
    baseline_hits = []
    for run in sense_runs:
        steps = run.get("steps", [])
        if step_idx < len(steps):
            checks = steps[step_idx].get("checks", [])
            if check_idx < len(checks):
                sense_hits.append(checks[check_idx].get("hit", False))
    for run in baseline_runs:
        steps = run.get("steps", [])
        if step_idx < len(steps):
            checks = steps[step_idx].get("checks", [])
            if check_idx < len(checks):
                baseline_hits.append(checks[check_idx].get("hit", False))

    if not sense_hits or not baseline_hits:
        return None, None, None

    sense_rate = sum(sense_hits) / len(sense_hits)
    baseline_rate = sum(baseline_hits) / len(baseline_hits)
    diff = sense_rate - baseline_rate
    return diff, sense_rate, baseline_rate


def extract_tool_usage(scored_runs):
    if not scored_runs:
        return {}
    totals = {"mcp": 0, "grep": 0, "read": 0, "total": 0, "runs": len(scored_runs)}
    for run in scored_runs:
        m = run.get("metrics", {})
        totals["mcp"] += m.get("mcp_count", 0)
        totals["grep"] += m.get("grep_count", 0)
        totals["read"] += m.get("read_count", 0)
        totals["total"] += m.get("tool_calls", 0)

    n = len(scored_runs)
    return {
        "mcp": round(totals["mcp"] / n, 1),
        "grep": round(totals["grep"] / n, 1),
        "read": round(totals["read"] / n, 1),
        "total": round(totals["total"] / n, 1),
        "mcp_ratio": round(totals["mcp"] / max(totals["total"], 1), 3),
        "runs": n,
    }


def extract_quality_signals(results_dir, tool, repo):
    base = os.path.join(results_dir, tool, repo)
    transcript_path = os.path.join(base, "transcript.json")
    if not os.path.exists(transcript_path):
        for entry in sorted(os.listdir(base)):
            if entry.startswith("run-"):
                tp = os.path.join(base, entry, "transcript.json")
                if os.path.exists(tp):
                    transcript_path = tp
                    break
    if not os.path.exists(transcript_path):
        return {}

    # Use the audit-text view (assistant answer + tool inputs + tool results)
    # so file refs that surface only inside tool output (grep paths, Read
    # targets) get counted. scorer.read_transcript_texts returns
    # (answer_text, audit_text).
    _, text = read_transcript_texts(transcript_path)

    matches = _SOURCE_FILE_RE.findall(text)
    excluded_exts = {'.md', '.txt', '.json', '.yaml', '.yml', '.toml',
                     '.lock', '.html', '.css', '.scss'}
    unique_files = set()
    for filepath, ref in matches:
        ext = os.path.splitext(filepath)[1].lower()
        if ext not in excluded_exts:
            unique_files.add(filepath)

    line_refs = re.findall(
        r'[\w/\-_.]+\.(?:py|go|rs|java|kt|rb|ts|tsx|js|jsx)\s*:\s*\d+',
        text
    )

    cross_file = re.findall(
        r'(?:calls|invokes|imports?\s+from|delegates?\s+to|defined\s+in|from\s+\w+\.(?:py|go|rs|java|rb|ts))',
        text, re.IGNORECASE
    )

    return {
        "unique_files": len(unique_files),
        "line_specificity": len(line_refs),
        "cross_file_connections": len(cross_file),
    }


def _avg_score(runs, key="fairness_score"):
    vals = [r.get(key) for r in runs if r.get(key) is not None]
    return round(sum(vals) / len(vals), 4) if vals else None


def _fairness_for_run(results_dir, tool, repo, scored_run):
    """Compute fairness_score from scored.json + judged.json via
    fairness.compute. scored.json doesn't store the combined score
    (the reporter / loop derives it on read), so analyze-transcripts
    has to do the same dance to get a meaningful per-run number.

    Returns the float score or None if judge data is missing.
    """
    from fairness import compute as fairness_compute

    # Find the corresponding judged.json. scored_run carries no path —
    # but it lives next to judged.json in the same result_dir.
    # Convention: load_scored_results returns runs in order; result_dirs
    # are tool/repo (single run) or tool/repo/run-N. We rebuild the path.
    judged = None
    for candidate in (
        os.path.join(results_dir, tool, repo, "judged.json"),
    ):
        if os.path.exists(candidate):
            with open(candidate) as f:
                judged = json.load(f)
            break
    if judged is None:
        return None
    out = fairness_compute(scored_run, judged)
    return out.get("score")


def _avg_fairness(results_dir, tool, repo, scored_runs):
    """Average fairness_score across runs for a (tool, repo). Uses
    fairness.compute, not the legacy `fairness_score` field on
    scored.json (which never gets populated)."""
    vals = []
    for run in scored_runs:
        s = _fairness_for_run(results_dir, tool, repo, run)
        if s is not None:
            vals.append(s)
    return round(sum(vals) / len(vals), 4) if vals else None


def analyze(results_dir, scenarios_dir):
    repos = discover_repos(results_dir)
    tools = discover_tools(results_dir)

    repo_analyses = {}
    summary = {
        "total_checks": 0,
        "non_differentiating": 0,
        "sense_advantage": 0,
        "baseline_advantage": 0,
    }

    for repo in repos:
        sense_runs = load_scored_results(results_dir, "sense", repo)
        baseline_runs = load_scored_results(results_dir, "baseline", repo)

        if not sense_runs or not baseline_runs:
            continue

        ref_run = sense_runs[0]
        checks_analysis = []

        for si, step in enumerate(ref_run.get("steps", [])):
            for ci, check in enumerate(step.get("checks", [])):
                diff, s_rate, b_rate = check_differentiation(
                    sense_runs, baseline_runs, si, ci
                )
                if diff is None:
                    continue

                summary["total_checks"] += 1
                if abs(diff) < 0.01:
                    rec = "non_differentiating"
                    if s_rate > 0.5:
                        rec += "_both_pass"
                    else:
                        rec += "_both_fail"
                    summary["non_differentiating"] += 1
                elif diff > 0:
                    rec = "sense_advantage"
                    summary["sense_advantage"] += 1
                else:
                    rec = "baseline_advantage"
                    summary["baseline_advantage"] += 1

                checks_analysis.append({
                    "step": step.get("name", f"step_{si}"),
                    "step_idx": si,
                    "check_idx": ci,
                    "type": check.get("type", ""),
                    "value": check.get("value", ""),
                    "required": check.get("required", True),
                    "sense_rate": round(s_rate, 3),
                    "baseline_rate": round(b_rate, 3),
                    "differentiation": round(diff, 3),
                    "recommendation": rec,
                })

        tool_usage = {}
        for tool in tools:
            runs = load_scored_results(results_dir, tool, repo)
            tool_usage[tool] = extract_tool_usage(runs)

        quality_signals = {}
        for tool in tools:
            quality_signals[tool] = extract_quality_signals(results_dir, tool, repo)

        sense_score = _avg_fairness(results_dir, "sense", repo, sense_runs)
        baseline_score = _avg_fairness(results_dir, "baseline", repo, baseline_runs)

        repo_analyses[repo] = {
            "checks": checks_analysis,
            "tool_usage": tool_usage,
            "quality_signals": quality_signals,
            "current_scores": {
                "sense": sense_score,
                "baseline": baseline_score,
                "gap": round((sense_score or 0) - (baseline_score or 0), 4),
            },
        }

    return {
        "repos": repo_analyses,
        "summary": summary,
    }


def main():
    parser = argparse.ArgumentParser(description="Analyze bench2 transcripts")
    parser.add_argument("--results-dir", default=os.path.join(BENCH2_DIR, "results"))
    parser.add_argument("--scenarios-dir", default=os.path.join(BENCH2_DIR, "scenarios"))
    parser.add_argument("--output", default=None, help="Output path (default: stdout)")
    args = parser.parse_args()

    result = analyze(args.results_dir, args.scenarios_dir)

    output = json.dumps(result, indent=2)
    if args.output:
        os.makedirs(os.path.dirname(args.output), exist_ok=True)
        with open(args.output, "w") as f:
            f.write(output)
            f.write("\n")
        print(f"Analysis written to {args.output}", file=sys.stderr)
    else:
        print(output)


if __name__ == "__main__":
    main()
