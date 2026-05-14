#!/usr/bin/env python3
"""Anti-Goodhart watchdog — did real quality move, or only the metric?

Two subcommands:

  snapshot --results-root <path> --out <path>
      Capture {tool: {repo: {keyword_coverage, fairness_score, llm_quality}}}
      from scored.json + judged.json under results-root. Idempotent.

  audit --before-snapshot <path> --after-snapshot <path>
        --improvements <improvements.json> --out <audit-watchdog.json>
      Compare two snapshots, compute deltas, ask the judge for a verdict.

A "suspect" verdict fires when llm_quality is flat or moved less than
keyword_coverage. Two consecutive suspect verdicts are a hard signal for
20-07's convergence logic; this pitch produces the flag only.
"""

import argparse
import json
import os
import sys

LIB_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, LIB_DIR)

from judge import (  # noqa: E402
    JUDGE_MODEL,
    call_judge,
    extract_judge_json,
)

PROMPT_VERSION = "v1"
TOOLS = ("sense", "baseline")
SUSPECT_THRESHOLD = 0.02

SYSTEM_PROMPT = """You are the anti-Goodhart watchdog for an autonomous
benchmark-tuning loop. Each iteration of the loop modifies *checks* (the
mechanical rules that produce keyword_coverage and fairness_score), then
re-runs scenarios.

The risk: the loop optimises checks against keyword presence, not against
genuine answer quality. If keyword_coverage rises but llm_quality (the
LLM judge's score against the per-scenario rubric) stays flat, the
iteration moved the metric without moving capability — Goodhart's law in
action.

You will receive:

- per-tool aggregate deltas: keyword_coverage and llm_quality before/after
- per-repo breakdown
- the iteration's improvements.json (what the loop changed)

Decide one of:

- "pass"    — llm_quality moved at least as much as keyword_coverage, OR
              keyword_coverage barely moved (no metric drift to be
              suspicious of)
- "suspect" — keyword_coverage moved noticeably more than llm_quality;
              the iteration plausibly chased the metric
- "neutral" — too little movement either way to judge

Be specific in the reason. Quote a concrete number pair. One short
sentence is enough.

Return a JSON object exactly of this shape, no markdown fences:

{
  "verdict": "pass" | "suspect" | "neutral",
  "reason": "<one sentence, with at least one number pair>",
  "flagged_for_human_review": <true if suspect>
}
"""


# ── Snapshot ───────────────────────────────────────────────────────────


def make_snapshot(results_root):
    """Sweep results-root and capture per-tool/repo metrics."""
    snapshot = {}
    for tool in TOOLS:
        tool_dir = os.path.join(results_root, tool)
        if not os.path.isdir(tool_dir):
            continue
        snapshot[tool] = {}
        for repo in sorted(os.listdir(tool_dir)):
            repo_dir = os.path.join(tool_dir, repo)
            if not os.path.isdir(repo_dir):
                continue
            scored_path = os.path.join(repo_dir, "scored.json")
            judged_path = os.path.join(repo_dir, "judged.json")
            if not os.path.exists(scored_path):
                continue
            with open(scored_path) as f:
                scored = json.load(f)
            entry = {
                "keyword_coverage": scored.get("keyword_coverage", 0.0),
                "fairness_score": scored.get("step_avg_score", 0.0),
                "failed": bool(scored.get("failed", False)),
            }
            if os.path.exists(judged_path):
                with open(judged_path) as f:
                    judged = json.load(f)
                entry["llm_quality"] = judged.get("scenario_quality", 0.0)
            else:
                entry["llm_quality"] = None
            snapshot[tool][repo] = entry
    return snapshot


def cmd_snapshot(args):
    snapshot = make_snapshot(args.results_root)
    with open(args.out, "w") as f:
        json.dump(snapshot, f, indent=2)
        f.write("\n")
    print(f"watchdog snapshot → {args.out}", file=sys.stderr)


# ── Deltas ─────────────────────────────────────────────────────────────


def aggregate_metric(snapshot, key):
    """Mean of a metric across all tools and repos, skipping Nones/failed."""
    values = []
    for tool, repos in snapshot.items():
        for repo, entry in repos.items():
            if entry.get("failed"):
                continue
            v = entry.get(key)
            if v is None:
                continue
            values.append(float(v))
    if not values:
        return None
    return round(sum(values) / len(values), 4)


def per_repo_deltas(before, after):
    rows = []
    for tool in TOOLS:
        b_repos = before.get(tool, {})
        a_repos = after.get(tool, {})
        for repo in sorted(set(b_repos) | set(a_repos)):
            b = b_repos.get(repo, {})
            a = a_repos.get(repo, {})
            row = {"tool": tool, "repo": repo}
            for key in ("keyword_coverage", "fairness_score", "llm_quality"):
                bv, av = b.get(key), a.get(key)
                row[f"{key}_before"] = bv
                row[f"{key}_after"] = av
                if bv is not None and av is not None:
                    row[f"{key}_delta"] = round(av - bv, 4)
                else:
                    row[f"{key}_delta"] = None
            rows.append(row)
    return rows


# ── Audit ──────────────────────────────────────────────────────────────


def cmd_audit(args):
    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        raise SystemExit("audit_watchdog: ANTHROPIC_API_KEY not set")

    with open(args.before_snapshot) as f:
        before = json.load(f)
    with open(args.after_snapshot) as f:
        after = json.load(f)

    improvements = {}
    if args.improvements and os.path.exists(args.improvements):
        with open(args.improvements) as f:
            improvements = json.load(f)

    aggregates = {
        "keyword_coverage_before": aggregate_metric(before, "keyword_coverage"),
        "keyword_coverage_after": aggregate_metric(after, "keyword_coverage"),
        "llm_quality_before": aggregate_metric(before, "llm_quality"),
        "llm_quality_after": aggregate_metric(after, "llm_quality"),
    }
    kc_b = aggregates["keyword_coverage_before"] or 0.0
    kc_a = aggregates["keyword_coverage_after"] or 0.0
    lq_b = aggregates["llm_quality_before"]
    lq_a = aggregates["llm_quality_after"]

    delta_kc = round(kc_a - kc_b, 4)
    delta_lq = (
        round((lq_a or 0.0) - (lq_b or 0.0), 4) if lq_a is not None and lq_b is not None else None
    )

    # Deterministic pre-verdict: if delta_lq is meaningfully smaller than
    # delta_kc, the judge has prima facie evidence for "suspect". We still
    # let the judge call decide so it can pick up nuance from improvements.
    rows = per_repo_deltas(before, after)

    user_text = json.dumps(
        {
            "aggregate_deltas": {
                "keyword_coverage": delta_kc,
                "llm_quality": delta_lq,
                "suspect_threshold": SUSPECT_THRESHOLD,
            },
            "aggregates": aggregates,
            "per_repo": rows,
            "improvements_summary": _summarise_improvements(improvements),
        },
        indent=2,
    )

    response = call_judge(
        SYSTEM_PROMPT, user_text, api_key=api_key, max_tokens=400
    )
    parsed = extract_judge_json(response)
    usage = response.get("usage", {}) or {}

    verdict = parsed.get("verdict", "neutral")
    if verdict not in {"pass", "suspect", "neutral"}:
        verdict = "neutral"

    payload = {
        "audit": {"model": JUDGE_MODEL, "prompt_version": PROMPT_VERSION},
        "verdict": verdict,
        "reason": str(parsed.get("reason", "")).strip(),
        "flagged_for_human_review": bool(parsed.get("flagged_for_human_review",
                                                    verdict == "suspect")),
        "aggregate_deltas": {
            "keyword_coverage": delta_kc,
            "llm_quality": delta_lq,
        },
        "aggregates": aggregates,
        "per_repo": rows,
        "usage": {
            "input_tokens": usage.get("input_tokens", 0),
            "output_tokens": usage.get("output_tokens", 0),
            "cache_creation_input_tokens": usage.get("cache_creation_input_tokens", 0),
            "cache_read_input_tokens": usage.get("cache_read_input_tokens", 0),
        },
    }

    with open(args.out, "w") as f:
        json.dump(payload, f, indent=2)
        f.write("\n")

    print(
        f"Watchdog: verdict={verdict} "
        f"Δkeyword_coverage={delta_kc:+.4f} Δllm_quality={delta_lq if delta_lq is None else f'{delta_lq:+.4f}'} "
        f"→ {args.out}",
        file=sys.stderr,
    )


def _summarise_improvements(improvements):
    """Compress improvements.json into a per-repo action count for the judge."""
    summary = []
    for scen in improvements.get("scenarios", []):
        actions = {}
        for mod in scen.get("modifications", []):
            actions[mod.get("action", "?")] = actions.get(mod.get("action", "?"), 0) + 1
        summary.append({
            "repo": scen.get("repo"),
            "actions": actions,
        })
    return summary


# ── CLI ────────────────────────────────────────────────────────────────


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("snapshot", help="capture current results into a snapshot")
    s.add_argument("--results-root", required=True)
    s.add_argument("--out", required=True)
    s.set_defaults(func=cmd_snapshot)

    a = sub.add_parser("audit", help="run watchdog against before/after snapshots")
    a.add_argument("--before-snapshot", required=True)
    a.add_argument("--after-snapshot", required=True)
    a.add_argument("--improvements", default=None)
    a.add_argument("--out", required=True)
    a.set_defaults(func=cmd_audit)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
