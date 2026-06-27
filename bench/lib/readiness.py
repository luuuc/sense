"""bench-readiness.md generator — the loop's real deliverable.

One page, plain language, citing the convergence block as evidence,
deciding "this benchmark is ready to score code-intel tools" or "not
yet — here's what's holding it back".

Emitted on any clean halt of the improvement loop (success, cost ceiling,
max iters, watchdog suspect, held-out mismatch, human SIGINT).

Usage:
    python3 bench/lib/readiness.py --loop-dir PATH \\
        --halt-reason REASON [--output PATH]
"""

from __future__ import annotations

import argparse
import glob
import json
import os
import sys
from typing import Any

LIB_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, LIB_DIR)
import convergence  # noqa: E402


def _maybe_load(path: str) -> Any | None:
    if not os.path.exists(path):
        return None
    with open(path) as f:
        return json.load(f)


def _latest_iter_dir(loop_dir: str) -> str | None:
    """Find the highest-numbered loop-1-iter-N directory under loop_dir."""
    pattern = os.path.join(loop_dir, "loop-1-iter-*")
    iters: list[tuple[int, str]] = []
    for d in glob.glob(pattern):
        name = os.path.basename(d)
        try:
            n = int(name.split("-")[-1])
        except ValueError:
            continue
        iters.append((n, d))
    if not iters:
        return None
    return max(iters, key=lambda t: t[0])[1]


def _prev_iter_dir(loop_dir: str, current_n: int) -> str | None:
    if current_n <= 1:
        return None
    candidate = os.path.join(loop_dir, f"loop-1-iter-{current_n - 1}")
    return candidate if os.path.isdir(candidate) else None


_HALT_VERDICT = {
    "converged": ("READY", "Four convergence criteria held for two iterations running."),
    "cost_ceiling": (
        "NOT READY",
        "Loop hit the per-loop cost ceiling before converging. "
        "Either raise the ceiling and resume or accept the partial result.",
    ),
    "max_iterations": (
        "NOT READY",
        "Loop reached --iterations without all four criteria holding. "
        "Inspect the Distance block to see which criteria are still failing.",
    ),
    "watchdog_suspect": (
        "NOT READY",
        "Watchdog flagged two consecutive iterations as suspect — the bench is "
        "drifting in a way the anti-Goodhart heuristic cannot endorse. Pause "
        "and re-grade held-out before resuming.",
    ),
    "held_out_mismatch": (
        "PANIC",
        "Held-out lockfile hashes do not match the committed held-out files. "
        "The bench's anchor has been disturbed — every score after the "
        "lockfile was written should be discarded until the held-out set is "
        "restored and the lockfile is regenerated.",
    ),
    "sigint": (
        "INDETERMINATE",
        "Human interrupted the loop. Inspect the latest iter's delta.md to "
        "decide whether to resume or treat as final.",
    ),
    "credit_exhausted": (
        "NOT READY",
        "API credit ran out. The loop continued scenario sessions on "
        "subscription, but Phase 4 audits were skipped — the auditor signals "
        "needed for criterion 1 are missing for the affected iterations.",
    ),
    "unknown": ("INDETERMINATE", "Halt reason not recognised."),
}


def render(
    loop_dir: str,
    halt_reason: str,
    held_out_dir: str | None = None,
) -> str:
    iter_dir = _latest_iter_dir(loop_dir)
    if iter_dir is None:
        return (
            "# bench-readiness — INDETERMINATE\n\n"
            f"No iteration directories found under `{loop_dir}`. "
            f"Halt reason: `{halt_reason}`.\n"
        )

    iter_n = int(os.path.basename(iter_dir).split("-")[-1])
    prev_dir = _prev_iter_dir(loop_dir, iter_n)
    conv = convergence.evaluate(iter_dir, prev_dir, held_out_dir)
    distance_block = convergence.format_distance_block(conv)

    verdict, verdict_detail = _HALT_VERDICT.get(halt_reason, _HALT_VERDICT["unknown"])

    # Cost summary, if present
    cost_state = _maybe_load(os.path.join(loop_dir, "loop-1", "cost.json"))
    if cost_state:
        cost_line = (
            f"Cost: cumulative ${cost_state.get('cumulative_usd', 0):.2f} "
            f"over {len(cost_state.get('iterations', []))} iteration(s)"
        )
    else:
        cost_line = "Cost: no cost.json found (was the loop ever run?)"

    # Discrimination snapshot from the latest post-analysis
    pa = _maybe_load(os.path.join(iter_dir, "post-analysis.json")) or {}
    repo_gaps = []
    for repo, info in (pa.get("repos") or {}).items():
        gap = (info.get("current_scores") or {}).get("gap")
        if gap is not None:
            repo_gaps.append((repo, gap))
    repo_gaps.sort(key=lambda t: t[1], reverse=True)

    repo_table_lines = ["| repo | fairness gap |", "|---|---|"]
    for repo, gap in repo_gaps:
        repo_table_lines.append(f"| {repo} | {gap:+.3f} |")
    repo_table = "\n".join(repo_table_lines) if repo_gaps else "*(no post-analysis available)*"

    out = [
        f"# bench-readiness — {verdict}",
        "",
        f"**Halt reason:** `{halt_reason}` — {verdict_detail}",
        "",
        f"**Latest iteration:** {os.path.basename(iter_dir)}",
        "",
        cost_line,
        "",
        "## Convergence snapshot",
        "",
        distance_block,
        "",
        "## Fairness gap by scenario",
        "",
        repo_table,
        "",
        "## What this means",
        "",
    ]

    if verdict == "READY":
        out.append(
            "The bench distinguishes code-intelligence tools from baseline in a "
            "way that survives auditor scrutiny and tracks human judgment. "
            "Scores produced by this configuration are safe to publish."
        )
    elif verdict == "PANIC":
        out.append(
            "Stop. Restore the held-out set, regenerate the lockfile, and "
            "discard any score produced since the lockfile was last valid."
        )
    else:
        unmet = [
            name
            for name, c in conv["criteria"].items()
            if not c["pass"] and not c.get("deferred")
        ]
        deferred = [
            name for name, c in conv["criteria"].items() if c.get("deferred")
        ]
        if unmet:
            out.append("**Unmet criteria:**")
            for name in unmet:
                out.append(f"- `{name}`")
            out.append("")
        if deferred:
            out.append("**Deferred criteria (insufficient data):**")
            for name in deferred:
                out.append(f"- `{name}`")
            out.append("")
        out.append(
            "Resume by addressing the unmet criteria. The deferred ones will "
            "resolve once the missing inputs (audit outputs, held-out re-score) "
            "land."
        )

    return "\n".join(out) + "\n"


# --- CLI -------------------------------------------------------------------

def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--loop-dir", required=True, help="directory with per-iteration loop results")
    p.add_argument(
        "--halt-reason",
        required=True,
        choices=sorted(_HALT_VERDICT),
        help="why the loop stopped — drives the verdict and the explanatory text",
    )
    p.add_argument("--held-out-dir", default=None)
    p.add_argument(
        "--output",
        default=None,
        help="path for bench-readiness.md (default: loop_dir/bench-readiness.md)",
    )
    args = p.parse_args()

    text = render(args.loop_dir, args.halt_reason, args.held_out_dir)
    out_path = args.output or os.path.join(args.loop_dir, "bench-readiness.md")
    os.makedirs(os.path.dirname(out_path) or ".", exist_ok=True)
    with open(out_path, "w") as f:
        f.write(text)
    print(f"wrote {out_path}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
