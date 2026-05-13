#!/usr/bin/env python3
"""Render meta-report.md from a Phase 4 iteration directory.

Usage: meta_report.py --iter-dir <path> --iter <N> [--out <path>]

The report is designed to be glanced at by a tired human on Sunday
evening: the most important facts live in the first three lines of each
section. No buried links. Four sections, in order: score auditor,
scenario auditor, watchdog, iteration outcome.
"""

import argparse
import glob
import json
import os
import sys


def load_json(path):
    if not os.path.exists(path):
        return None
    with open(path) as f:
        return json.load(f)


# ── Section: score auditor ─────────────────────────────────────────────


def section_score_auditor(iter_dir):
    files = sorted(glob.glob(os.path.join(iter_dir, "audit-scoring.*.json")))
    files = [f for f in files if not f.endswith("-full.json")]
    if not files:
        return "## Score auditor\n\n_no audit-scoring.*.json files in this iteration._\n"

    total = agreed = disagreed = unsure = 0
    over_threshold = []
    top_disagreement = None
    top_conf = -1.0
    rate_threshold = 0.05

    for path in files:
        data = load_json(path)
        if not data:
            continue
        if data.get("skipped_reason"):
            continue
        total += data.get("total_checks", 0)
        agreed += data.get("agreed", 0)
        disagreed += data.get("disagreed", 0)
        unsure += data.get("unsure", 0)
        rate_threshold = data.get("rate_threshold", rate_threshold)
        if data.get("over_threshold"):
            over_threshold.append({
                "tool": data.get("tool"),
                "repo": data.get("repo"),
                "rate": data.get("disagreement_rate"),
            })
        for d in data.get("disagreements", []):
            if d.get("confidence", 0) > top_conf:
                top_conf = d["confidence"]
                top_disagreement = {**d, "tool": data.get("tool"),
                                    "repo": data.get("repo")}

    rate = round((disagreed + unsure) / total, 4) if total else 0.0
    status = "under threshold ✓" if rate <= rate_threshold else (
        f"OVER threshold ({rate_threshold}) ⚠"
    )

    lines = [
        "## Score auditor",
        "",
        f"Agreement: {agreed}/{total} ({rate*100:.1f}% disagreement+unsure) — {status}",
    ]
    if over_threshold:
        lines.append("")
        lines.append(f"Transcripts over threshold ({len(over_threshold)}):")
        for o in over_threshold:
            lines.append(f"- {o['tool']}/{o['repo']}: {o['rate']:.3f}")
    if top_disagreement:
        lines.append("")
        d = top_disagreement
        check = d.get("check", {})
        lines.append(
            f"Top disagreement: {d.get('tool')}/{d.get('repo')} / "
            f"{d.get('step', '?')} / "
            f"`{check.get('type')}={check.get('value')!r}` "
            f"(engine={d.get('engine_verdict')}, judge={d.get('judge_verdict')}, "
            f"conf={d.get('confidence', 0):.2f})"
        )
        if d.get("rationale"):
            lines.append(f"  → {d['rationale']}")
    lines.append("")
    return "\n".join(lines)


# ── Section: scenario auditor ─────────────────────────────────────────


def section_scenario_auditor(iter_dir):
    files = sorted(glob.glob(os.path.join(iter_dir, "audit-scenarios.*.json")))
    if not files:
        return "## Scenario auditor\n\n_no audit-scenarios.*.json files in this iteration._\n"

    total_non_disc = 0
    total_missing = 0
    per_repo = []
    for path in files:
        data = load_json(path)
        if not data:
            continue
        n = len(data.get("non_discriminating_checks", []))
        m = len(data.get("missing_signals", []))
        total_non_disc += n
        total_missing += m
        per_repo.append((data.get("repo"), n, m))

    lines = [
        "## Scenario auditor",
        "",
        f"{total_non_disc} non-discriminating checks proposed for removal.",
        f"{total_missing} missing-signal checks proposed for addition.",
        f"Forwarded to next iteration's improvements.json candidates.",
        "",
    ]
    if per_repo:
        lines.append("Per repo:")
        for repo, n, m in per_repo:
            lines.append(f"- {repo}: {n} non-discriminating, {m} missing signals")
        lines.append("")
    return "\n".join(lines)


# ── Section: watchdog ─────────────────────────────────────────────────


def section_watchdog(iter_dir):
    data = load_json(os.path.join(iter_dir, "audit-watchdog.json"))
    if not data:
        return "## Watchdog\n\n_no audit-watchdog.json in this iteration._\n"

    verdict = data.get("verdict", "neutral")
    icon = {"pass": "✓", "suspect": "⚠", "neutral": "·"}.get(verdict, "?")
    deltas = data.get("aggregate_deltas", {})
    dkc = deltas.get("keyword_coverage")
    dlq = deltas.get("llm_quality")

    def fmt(v):
        return f"{v:+.4f}" if isinstance(v, (int, float)) else str(v)

    lines = [
        "## Watchdog",
        "",
        f"Verdict: {verdict} {icon}",
        f"Δkeyword_coverage={fmt(dkc)}, Δllm_quality={fmt(dlq)}",
    ]
    if data.get("reason"):
        lines.append(f"Reason: {data['reason']}")
    if data.get("flagged_for_human_review"):
        lines.append("Flagged for human review.")
    lines.append("")
    return "\n".join(lines)


# ── Section: iteration outcome ─────────────────────────────────────────


def section_iteration_outcome(iter_dir, iter_num):
    regression = load_json(os.path.join(iter_dir, "regression.json"))
    if regression is None:
        outcome = "unknown"
    elif regression.get("regressed"):
        outcome = "Rolled back. Scenarios reverted to pre-iter state."
    else:
        outcome = "Kept. Improvements forwarded to next iteration."

    return "\n".join([
        "## Iteration outcome",
        "",
        outcome,
        "",
    ])


# ── Render ─────────────────────────────────────────────────────────────


def render(iter_dir, iter_num):
    return "\n".join([
        f"# Iter {iter_num} — Meta Report",
        "",
        section_score_auditor(iter_dir),
        section_scenario_auditor(iter_dir),
        section_watchdog(iter_dir),
        section_iteration_outcome(iter_dir, iter_num),
    ])


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--iter-dir", required=True)
    parser.add_argument("--iter", required=True)
    parser.add_argument("--out", default=None)
    args = parser.parse_args()

    out_path = args.out or os.path.join(args.iter_dir, "meta-report.md")
    body = render(args.iter_dir, args.iter)
    with open(out_path, "w") as f:
        f.write(body)
        if not body.endswith("\n"):
            f.write("\n")
    print(f"meta-report → {out_path}", file=sys.stderr)


if __name__ == "__main__":
    main()
