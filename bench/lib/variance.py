#!/usr/bin/env python3
"""Judge variance baseline.

Runs judge.py twice over a fixed set of transcripts (different scenarios,
different tools), then compares per-step criterion scores. Writes a
markdown summary with per-criterion absolute deltas and a target check
of stdev < 0.05.

Usage: variance.py <bench_dir> <out_md>

Reads ANTHROPIC_API_KEY from env; judge.py does the API calls.
"""

import json
import math
import os
import subprocess
import sys


TRANSCRIPTS = [
    ("sense", "discourse"),
    ("baseline", "discourse"),
    ("sense", "flask"),
    ("sense", "axum"),
]


def run_judge(bench_dir, tool, repo, run_idx):
    result_dir = os.path.join(bench_dir, "results", tool, repo)
    scored = os.path.join(result_dir, "scored.json")
    transcript = os.path.join(result_dir, "transcript.json")
    rubric = os.path.join(bench_dir, "scenarios", f"{repo}.rubric.yaml")
    out_path = os.path.join(result_dir, f"judged.var{run_idx}.json")

    cmd = [
        "python3", os.path.join(bench_dir, "lib", "judge.py"),
        scored, transcript, rubric, "--out", out_path,
    ]
    result = subprocess.run(cmd, capture_output=True, text=True, env=os.environ)
    if result.returncode != 0:
        print(result.stdout, file=sys.stderr)
        print(result.stderr, file=sys.stderr)
        # Propagate credit-exhausted (42) verbatim so the orchestrator
        # can short-circuit the rest of the iteration. Any other non-zero
        # is a hard failure.
        if result.returncode == 42:
            sys.exit(42)
        raise SystemExit(f"judge.py failed for {tool}/{repo} run {run_idx}")
    return out_path


def stdev_n2(a, b):
    """Sample stdev for n=2."""
    return abs(a - b) / math.sqrt(2)


def compare(run1_path, run2_path):
    """Return per-step / per-criterion comparison rows."""
    with open(run1_path) as f:
        r1 = json.load(f)
    with open(run2_path) as f:
        r2 = json.load(f)

    rows = []
    for s1, s2 in zip(r1["steps"], r2["steps"]):
        step_name = s1["step"]
        for crit in ("map_quality", "specificity", "justification", "uncertainty"):
            v1 = s1["scores"][crit]["score"]
            v2 = s2["scores"][crit]["score"]
            rows.append({
                "step": step_name,
                "criterion": crit,
                "run1": v1,
                "run2": v2,
                "delta": abs(v1 - v2),
                "stdev": stdev_n2(v1, v2),
            })
        # also step_quality
        rows.append({
            "step": step_name,
            "criterion": "step_quality",
            "run1": s1["step_quality"],
            "run2": s2["step_quality"],
            "delta": abs(s1["step_quality"] - s2["step_quality"]),
            "stdev": stdev_n2(s1["step_quality"], s2["step_quality"]),
        })
    return r1, r2, rows


def fmt_md_table(transcript_label, r1, r2, rows):
    lines = []
    lines.append(f"### {transcript_label}")
    lines.append("")
    lines.append(
        f"Scenario quality: run1 = {r1['scenario_quality']:.4f}, "
        f"run2 = {r2['scenario_quality']:.4f}, "
        f"|Δ| = {abs(r1['scenario_quality'] - r2['scenario_quality']):.4f}"
    )
    lines.append("")
    lines.append("| Step | Criterion | run1 | run2 | |Δ| | stdev (n=2) |")
    lines.append("|------|-----------|-----:|-----:|---:|-----:|")
    for row in rows:
        flag = " ⚠" if row["stdev"] > 0.05 else ""
        lines.append(
            f"| {row['step'][:48]} | {row['criterion']} | "
            f"{row['run1']:.2f} | {row['run2']:.2f} | "
            f"{row['delta']:.3f} | {row['stdev']:.3f}{flag} |"
        )
    lines.append("")
    return "\n".join(lines)


def main(argv):
    if len(argv) < 3:
        print("Usage: variance.py <bench_dir> <out_md>", file=sys.stderr)
        sys.exit(1)

    bench_dir = os.path.abspath(argv[1])
    out_md = argv[2]

    if not os.environ.get("ANTHROPIC_API_KEY"):
        raise SystemExit("variance: ANTHROPIC_API_KEY not set")

    md_parts = ["# Judge variance baseline", ""]
    md_parts.append(
        "Two runs of judge.py against the same transcripts, same rubric, "
        "same prompt. `temperature` is omitted from the request — "
        "deprecated on `claude-opus-4-7` — so the model uses its default "
        "sampling mode and the deltas below characterise the residual "
        "non-determinism we get out of the box."
    )
    md_parts.append("")
    md_parts.append(
        "**Target (from pitch 20-05):** per-criterion stdev < 0.05. "
        "If we fail it, the next move is either to bump samples per call "
        "(deferred to 20-06), tighten the rubric question wording, or "
        "anchor the score scale (e.g. snap to {0.0, 0.25, 0.5, 0.75, 1.0})."
    )
    md_parts.append("")

    summary = []
    all_stdevs = {"map_quality": [], "specificity": [], "justification": [],
                  "uncertainty": [], "step_quality": []}

    for tool, repo in TRANSCRIPTS:
        label = f"{tool}/{repo}"
        print(f"→ judging {label} run 1...", file=sys.stderr)
        p1 = run_judge(bench_dir, tool, repo, 1)
        print(f"→ judging {label} run 2...", file=sys.stderr)
        p2 = run_judge(bench_dir, tool, repo, 2)

        r1, r2, rows = compare(p1, p2)
        md_parts.append(fmt_md_table(label, r1, r2, rows))

        for row in rows:
            all_stdevs[row["criterion"]].append(row["stdev"])

        sq_delta = abs(r1["scenario_quality"] - r2["scenario_quality"])
        summary.append((label, r1["scenario_quality"], r2["scenario_quality"], sq_delta))

    md_parts.append("## Pooled stdev across all steps")
    md_parts.append("")
    md_parts.append("| Criterion | n | mean stdev | max stdev | passes (<0.05) |")
    md_parts.append("|-----------|--:|----------:|---------:|:-------:|")
    for crit, vals in all_stdevs.items():
        n = len(vals)
        if n == 0:
            continue
        mean_sd = sum(vals) / n
        max_sd = max(vals)
        passes = "✓" if max_sd < 0.05 else "✗"
        md_parts.append(
            f"| {crit} | {n} | {mean_sd:.3f} | {max_sd:.3f} | {passes} |"
        )
    md_parts.append("")

    md_parts.append("## Scenario-quality stability")
    md_parts.append("")
    md_parts.append("| Transcript | run1 quality | run2 quality | |Δ| |")
    md_parts.append("|------------|-------------:|-------------:|---:|")
    for label, q1, q2, dq in summary:
        md_parts.append(f"| {label} | {q1:.4f} | {q2:.4f} | {dq:.4f} |")
    md_parts.append("")

    with open(out_md, "w") as f:
        f.write("\n".join(md_parts))
        f.write("\n")
    print(f"Variance report → {out_md}", file=sys.stderr)


if __name__ == "__main__":
    main(sys.argv)
