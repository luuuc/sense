"""Per-iteration delta report — `iter-N/delta.md`.

Designed so a human reading on the train can answer:
  - Did this iteration help?
  - Was it the bench or the tools that moved?
  - How close are we to done?
  - Should I stop the loop?

The "Distance from convergence" block (from lib.convergence) is the most
important text in the loop and is also printed to stdout at the end of
every iteration. This report wraps that block with score-deltas, the
bench-changes summary, and audit findings.

Usage:
    python3 bench/lib/delta_report.py --iter-dir PATH \\
        [--prev-iter-dir PATH] [--held-out-dir PATH] [--output PATH]
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


# --- IO helpers --------------------------------------------------------------

def _load(path: str) -> Any:
    with open(path) as f:
        return json.load(f)


def _maybe_load(path: str) -> Any | None:
    return _load(path) if os.path.exists(path) else None


def _fmt_delta(v: float | None, places: int = 2) -> str:
    if v is None:
        return "—"
    sign = "+" if v > 0 else ("" if v == 0 else "-")
    return f"{sign}{abs(v):.{places}f}"


# --- Section: Score changes -------------------------------------------------

def _score_changes(
    cur_pa: dict[str, Any], prev_pa: dict[str, Any] | None
) -> list[dict[str, Any]]:
    """For each repo with current_scores, produce a row with sense/baseline/gap
    and the iter-over-iter deltas. Quality + grounding deltas read from the
    rich `quality_signals` block when available.
    """
    rows: list[dict[str, Any]] = []
    cur_repos = cur_pa.get("repos", {})
    prev_repos = (prev_pa or {}).get("repos", {})

    for repo in sorted(cur_repos):
        cur = cur_repos[repo]
        prev = prev_repos.get(repo, {})
        cs = cur.get("current_scores") or {}
        ps = prev.get("current_scores") or {}

        cur_qual = (cur.get("quality_signals") or {}).get("llm_quality") or {}
        prev_qual = (prev.get("quality_signals") or {}).get("llm_quality") or {}
        cur_ground = (cur.get("quality_signals") or {}).get("citation_grounding") or {}
        prev_ground = (prev.get("quality_signals") or {}).get("citation_grounding") or {}

        def _delta(a: float | None, b: float | None) -> float | None:
            if a is None or b is None:
                return None
            return round(a - b, 4)

        rows.append(
            {
                "repo": repo,
                "sense": cs.get("sense"),
                "baseline": cs.get("baseline"),
                "gap": cs.get("gap"),
                "fairness_delta": _delta(cs.get("gap"), ps.get("gap")),
                "quality_delta": _delta(cur_qual.get("gap"), prev_qual.get("gap")),
                "grounding_delta": _delta(
                    cur_ground.get("gap"), prev_ground.get("gap")
                ),
            }
        )
    return rows


def _render_score_changes(rows: list[dict[str, Any]]) -> str:
    if not rows:
        return "## Score changes\n\n*(no post-analysis.json available)*\n"

    lines = [
        "## Score changes",
        "",
        "| scenario | sense | baseline | gap | fairness Δ | quality Δ | grounding Δ |",
        "|---|---|---|---|---|---|---|",
    ]
    for r in rows:
        lines.append(
            "| {repo} | {s} | {b} | {g} | {fd} | {qd} | {gd} |".format(
                repo=r["repo"],
                s=f"{r['sense']:.3f}" if r["sense"] is not None else "—",
                b=f"{r['baseline']:.3f}" if r["baseline"] is not None else "—",
                g=f"{r['gap']:+.3f}" if r["gap"] is not None else "—",
                fd=_fmt_delta(r["fairness_delta"]),
                qd=_fmt_delta(r["quality_delta"]),
                gd=_fmt_delta(r["grounding_delta"]),
            )
        )
    return "\n".join(lines)


# --- Section: What changed in the bench -------------------------------------

def _render_bench_changes(manifest: dict[str, Any] | None) -> str:
    if not manifest:
        return (
            "## What changed in the bench\n\n"
            "*(no changes-manifest.json — no improvements applied this iter)*\n"
        )

    lines = ["## What changed in the bench", ""]
    any_changes = False
    for fname in sorted(manifest):
        info = manifest[fname]
        if not isinstance(info, dict):
            continue
        added = info.get("checks_added", 0)
        removed = info.get("checks_removed", 0)
        tight = info.get("checks_tightened", 0)
        weights = info.get("weights_changed", False)
        if added == 0 and removed == 0 and tight == 0 and not weights:
            continue
        any_changes = True
        bits = []
        if added:
            bits.append(f"+{added} check{'s' if added != 1 else ''}")
        if removed:
            bits.append(f"-{removed} check{'s' if removed != 1 else ''}")
        if tight:
            bits.append(f"~{tight} tightened")
        if weights:
            bits.append("weights tuned")
        lines.append(f"- **{fname}** — {', '.join(bits)}")
        for detail in info.get("details", []) or []:
            lines.append(f"    - {detail}")
    if not any_changes:
        lines.append("- *(manifest present but no net changes)*")
    return "\n".join(lines)


# --- Section: Audit findings ------------------------------------------------

def _render_audit_findings(iter_dir: str) -> str:
    lines = ["## Audit findings", ""]

    # Score-auditor disagreement: pulled from convergence c1 indirectly by
    # re-using the same files. Keep this section human-friendly (rates per
    # tool/repo + average).
    sf = sorted(
        f
        for f in glob.glob(os.path.join(iter_dir, "audit-scoring.*.json"))
        if not f.endswith("-full.json")
    )
    if sf:
        rates: list[tuple[str, float, bool]] = []
        for path in sf:
            try:
                d = _load(path)
            except (OSError, json.JSONDecodeError):
                continue
            name = os.path.basename(path).removeprefix("audit-scoring.").removesuffix(
                ".json"
            )
            rate = d.get("disagreement_rate")
            over = d.get("over_threshold", False)
            if rate is None:
                continue
            rates.append((name, rate, over))
        if rates:
            avg = sum(r for _, r, _ in rates) / len(rates)
            lines.append(f"**Score-auditor disagreement:** {avg*100:.1f}% avg")
            for name, rate, over in rates:
                marker = " ⚠" if over else ""
                lines.append(f"  - {name}: {rate*100:.1f}%{marker}")
            lines.append("")
    else:
        lines.append("**Score-auditor:** no audit-scoring.*.json present.")
        lines.append("")

    # Watchdog verdict — aggregate the per-repo files into a single
    # tally + flag any flagged_for_human_review. The watchdog can be
    # per-repo (audit-watchdog.<repo>.json) or a single global file
    # (audit-watchdog.json) depending on how Phase 4 was invoked.
    wfiles = sorted(
        glob.glob(os.path.join(iter_dir, "audit-watchdog.*.json"))
        + glob.glob(os.path.join(iter_dir, "audit-watchdog.json"))
    )
    if wfiles:
        verdicts: list[tuple[str, str, str, bool]] = []
        for path in wfiles:
            try:
                d = _load(path)
            except (OSError, json.JSONDecodeError):
                continue
            name = os.path.basename(path)
            if name == "audit-watchdog.json":
                repo = "global"
            else:
                repo = name.removeprefix("audit-watchdog.").removesuffix(".json")
            verdicts.append(
                (
                    repo,
                    d.get("verdict", "?"),
                    d.get("reason", ""),
                    d.get("flagged_for_human_review", False),
                )
            )
        if verdicts:
            lines.append("**Watchdog:**")
            for repo, verdict, reason, flagged in verdicts:
                marker = " 🚨" if flagged else ""
                short = reason.split(".")[0] if reason else ""
                lines.append(f"  - {repo}: {verdict}{marker} — {short}")
            lines.append("")
    else:
        lines.append("**Watchdog:** no audit-watchdog.*.json present.")
        lines.append("")

    # Scenario auditor — just count proposals
    sn_files = sorted(glob.glob(os.path.join(iter_dir, "audit-scenarios.*.json")))
    if sn_files:
        total = 0
        per_repo: list[tuple[str, int]] = []
        for path in sn_files:
            try:
                d = _load(path)
            except (OSError, json.JSONDecodeError):
                continue
            props = d.get("proposals", []) or d.get("proposed_changes", [])
            n = len(props) if isinstance(props, list) else 0
            repo = os.path.basename(path).removeprefix("audit-scenarios.").removesuffix(
                ".json"
            )
            per_repo.append((repo, n))
            total += n
        lines.append(f"**Scenario auditor:** {total} proposal(s) across {len(per_repo)} repo(s)")
        for repo, n in per_repo:
            lines.append(f"  - {repo}: {n}")
    else:
        lines.append("**Scenario auditor:** no audit-scenarios.*.json present.")
    return "\n".join(lines)


# --- Full report -----------------------------------------------------------

def render_delta_report(
    iter_dir: str,
    prev_iter_dir: str | None,
    held_out_dir: str | None,
    iter_label: str | None = None,
) -> str:
    cur_pa = _maybe_load(os.path.join(iter_dir, "post-analysis.json")) or {"repos": {}}
    prev_pa = (
        _maybe_load(os.path.join(prev_iter_dir, "post-analysis.json"))
        if prev_iter_dir
        else None
    )
    manifest = _maybe_load(os.path.join(iter_dir, "changes-manifest.json"))

    score_rows = _score_changes(cur_pa, prev_pa)
    score_md = _render_score_changes(score_rows)
    bench_md = _render_bench_changes(manifest)
    audit_md = _render_audit_findings(iter_dir)

    conv_result = convergence.evaluate(iter_dir, prev_iter_dir, held_out_dir)
    distance_md = convergence.format_distance_block(conv_result)

    label = iter_label or os.path.basename(os.path.normpath(iter_dir))
    header = f"# {label} delta"
    return "\n\n".join([header, score_md, bench_md, audit_md, distance_md]) + "\n"


# --- CLI -------------------------------------------------------------------

def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--iter-dir", required=True)
    p.add_argument("--prev-iter-dir", default=None)
    p.add_argument("--held-out-dir", default=None)
    p.add_argument("--iter-label", default=None, help='heading label, e.g. "Iter 3"')
    p.add_argument("--output", default=None, help="path for delta.md (defaults to iter_dir/delta.md)")
    args = p.parse_args()

    out_path = args.output or os.path.join(args.iter_dir, "delta.md")
    text = render_delta_report(
        args.iter_dir,
        args.prev_iter_dir,
        args.held_out_dir,
        iter_label=args.iter_label,
    )
    with open(out_path, "w") as f:
        f.write(text)
    print(f"wrote {out_path}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
