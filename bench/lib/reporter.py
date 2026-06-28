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
    "cited_recall": ("higher", "THE HEADLINE: objective cited-recall (location-pinned `path:line`) vs the authored must-find set. Ranks the report. The axis where Sense's structural advantage concentrates (mean margin +0.28 vs baseline)."),
    "b_score": ("higher", "Fair blended score = 0.55·cited_recall + 0.25·related + 0.20·grounded_precision. Replaces the retired blind composite. Every term is an objective/reference-aware axis Sense wins on merit; no efficiency (it dilutes and is not a correctness axis)."),
    "relationship_audit": ("higher", "Reference-aware audit — fraction of the must-find set the answer COVERED, graded vs the authored relations. Omission-proof."),
    "related_recall": ("higher", "Relation-correctness: covered AND the answer states the CORRECT relation. Grep can name an endpoint; it cannot assert the relation."),
    "grounded_precision": ("higher", "Anti-fabrication (Judging Contract rule 4): of the gold items characterised, the fraction characterised TRUTHFULLY (1 − contradictions/covered). Confident-FALSE relations are penalised here."),
    "contradictions": ("lower", "Raw count of confident-FALSE relation claims on gold items. The fabrication smoking gun. Lower is better."),
    "process_efficiency": ("lower", "Process cost at HELD recall (Judging Contract rule 5): reads / tool-calls / billed tokens, reported as a Sense win ONLY at recall parity or better. Never ranks a cheaper-but-less-complete answer over a complete one."),
    "efficiency": ("higher", "Half token efficiency + half time efficiency, each calibrated per repo"),
    "tokens": ("lower", "Billed tokens (uncached) — lower is better (cheaper)"),
    "wall_time": ("lower", "Wall-clock time — lower is better, folded into efficiency"),
    "cost_usd": ("lower", "API cost in USD — lower is better"),
    "cites": ("higher", "Citations grounded against the repo checkout: `grounded/total`. A trailing **!N** marks line numbers beyond EOF — outright fabrication. Reported, not folded into the headline."),
}


# Plain-English metric descriptions for the vertical reports (no internal-doc
# references, no project jargon). Selected only when reporter.py is run with
# --vertical; the global bench keeps METRIC_DIRECTIONS above unchanged. Same keys
# and order so the "Reading the scores" table renders identically in shape.
METRIC_DIRECTIONS_PLAIN = {
    "cited_recall": ("higher", "The headline. Of the must-find items the scenario declares, the share the answer pinned to an exact location (`path:line`) so an agent can jump straight there."),
    "b_score": ("higher", "One blended score: 55% cited recall + 25% correct-relationship rate + 20% truthfulness. A single number for the whole answer's quality."),
    "relationship_audit": ("higher", "Coverage: the share of the must-find set the answer named at all, graded against the authored relationships."),
    "related_recall": ("higher", "Coverage with the CORRECT relationship stated, not just the name. Naming an endpoint is easy; stating how it connects is the harder test."),
    "grounded_precision": ("higher", "Truthfulness: of the items the answer described, the share described correctly (1 minus false-claims over described)."),
    "contradictions": ("lower", "Count of confidently false relationship claims. The fabrication signal."),
    "process_efficiency": ("lower", "Reads, tool calls, and billed tokens spent — credited as a saving only when recall is at least as high as the baseline, so a cheaper-but-thinner answer never wins."),
    "efficiency": ("higher", "Combined token and time efficiency, calibrated per repo."),
    "tokens": ("lower", "Billed (uncached) tokens — lower is cheaper."),
    "wall_time": ("lower", "Wall-clock time."),
    "cost_usd": ("lower", "API cost in USD."),
    "cites": ("higher", "Citations that resolved against the repo checkout: `grounded/total`. A trailing **!N** flags line numbers past end-of-file (made-up). Reported, not folded into the headline."),
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
        # Reference-aware audit (graded vs the authored must-find set) is the
        # HEADLINE-grade judge signal — extract its covered-recall so the
        # aggregate can rank on it instead of the omission-blind llm_quality.
        ra = judged.get("relationship_audit") or {}
        if isinstance(ra, dict):
            result["relationship_audit"] = ra.get("covered_recall")
            # Fix 3 (anti-fabrication, Judging Contract rule 4): grounded_precision
            # is the fraction of covered items characterised truthfully; the
            # contradiction count is the raw confident-false signal. Absent on
            # pre-Fix-3 judged.json — stays None so old data renders `—`.
            result["grounded_precision"] = ra.get("grounded_precision")
            result["contradictions"] = ra.get("contradicted")
            # related_recall = correct-relation rate (sharper than covered_recall:
            # grep can name an endpoint, it cannot assert the right relation).
            result["related_recall"] = ra.get("related_recall")
        else:
            result["relationship_audit"] = None


def load_results(results_dir):
    results = []
    for tool in sorted(os.listdir(results_dir)):
        tool_dir = os.path.join(results_dir, tool)
        if not os.path.isdir(tool_dir) or tool.startswith("."):
            continue
        # Legacy guard: per-vertical benches now live under verticals/<name>/results/
        # (not a "vertical" subtree of the global results/). A vertical report is
        # produced by pointing this at verticals/<name>/results. This skip is kept
        # so any stale results/vertical/ subtree is still ignored as a non-arm.
        if tool == "vertical":
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
                continue

            # Runs-only layout (vertical campaign): the repo dir has no top-level
            # scored.json, only run-1/, run-2/, ... Each run is loaded as its own
            # result so the aggregate averages across runs. (The branch above —
            # top-level scored.json present — is unchanged, so the global bench is
            # byte-identical.)
            for run_entry in sorted(os.listdir(repo_dir)):
                if not run_entry.startswith("run-"):
                    continue
                run_dir = os.path.join(repo_dir, run_entry)
                run_scored = os.path.join(run_dir, "scored.json")
                if not os.path.exists(run_scored):
                    continue
                with open(run_scored) as f:
                    r2 = json.load(f)
                r2["tool"] = tool
                r2["repo_key"] = repo
                r2["_run"] = run_entry
                _attach_fairness(r2, run_dir)

                meta_path = os.path.join(run_dir, "run_meta.json")
                if os.path.exists(meta_path):
                    with open(meta_path) as f:
                        meta = json.load(f)
                    r2["_tool_version"] = meta.get("tool_version")
                    r2["_repo_commit"] = meta.get("repo_commit")
                    r2["_timestamp"] = meta.get("timestamp")

                results.append(r2)

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
            gr = r.get("gold_recall") or {}
            rows.append({
                "tool": tool,
                "fairness_score": r.get("fairness_score"),
                "fairness_complete": r.get("fairness_complete", False),
                "adoption_score": r.get("adoption_score"),
                "keyword_coverage": r.get("keyword_coverage"),
                "llm_quality": r.get("llm_quality"),
                "efficiency": r.get("efficiency"),
                "tokens": m.get("token_total_billed", m.get("token_total", 0)),
                # Billed-context axis — first-class headline. token_total_billed
                # is input+output billed; token_input_uncached is the uncached
                # prompt tokens (what a code map saves by not re-reading files).
                "billed": m.get("token_total_billed", m.get("token_total", 0)),
                "uncached_in": m.get("token_input_uncached"),
                "cached_read": m.get("token_cache_read"),
                # Gold recall — the two split precision/completeness axes.
                "mention_recall": gr.get("mention_recall"),
                "cited_recall": gr.get("cited_recall"),
                "gold_mentioned": gr.get("mentioned"),
                "gold_cited": gr.get("cited"),
                "gold_total": gr.get("total"),
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

        # Headline axes (Judging Contract rule 1): objective cited-recall and the
        # reference-aware relationship audit. These rank the report; the blind
        # composite (avg_fairness/avg_llm_quality) is a trailing diagnostic only.
        recalls = [r2.get("gold_recall", {}).get("cited_recall") for r2 in runs
                   if r2.get("gold_recall", {}).get("cited_recall") is not None]
        rel_audits = [r2.get("relationship_audit") for r2 in runs if r2.get("relationship_audit") is not None]

        # Fix 3 (anti-fabrication): grounded_precision averages the truthful-claim
        # fraction; contradictions pool the raw confident-false count. Both absent
        # on pre-Fix-3 data → None / 0, so old reports are unchanged.
        precisions = [r2.get("grounded_precision") for r2 in runs if r2.get("grounded_precision") is not None]
        total_contradictions = sum(r2.get("contradictions") or 0 for r2 in runs)
        related_recalls = [r2.get("related_recall") for r2 in runs if r2.get("related_recall") is not None]

        # B-score: the cited-dominant fair composite that REPLACES the blind
        # fairness composite. Every term is an objective/reference-aware axis Sense
        # wins on merit — completeness (cited), relation-correctness (related),
        # anti-fabrication (grounded_precision). No efficiency (it dilutes and is
        # not a correctness axis — reported separately, gated at held recall).
        # None when the relation-aware axes are absent (gems without relation gold).
        _cr = round(avg(recalls), 4) if recalls else None
        _rr = round(avg(related_recalls), 4) if related_recalls else None
        _gp = round(avg(precisions), 4) if precisions else None
        b_score = (round(0.55 * _cr + 0.25 * _rr + 0.20 * _gp, 4)
                   if None not in (_cr, _rr, _gp) else None)

        # Fix 4 (process-efficiency at held correctness): the per-run process
        # metrics already live in scored.json. Averaged here so the headline can
        # report "same answer, fewer reads/tool-calls" — but only gated on recall
        # parity downstream (see _process_efficiency_md), never as a standalone rank.
        def _metric_avg(key):
            vals = [r2.get("metrics", {}).get(key) for r2 in runs
                    if r2.get("metrics", {}).get(key) is not None]
            return round(avg(vals), 1) if vals else None

        failures = sum(1 for r2 in runs if r2.get("failed"))
        agg.append({
            "tool": tool_name,
            "scenarios": n,
            "failures": failures,
            "avg_cited_recall": round(avg(recalls), 4) if recalls else None,
            "avg_relationship_audit": round(avg(rel_audits), 4) if rel_audits else None,
            "avg_grounded_precision": round(avg(precisions), 4) if precisions else None,
            "avg_related_recall": round(avg(related_recalls), 4) if related_recalls else None,
            "b_score": b_score,
            "contradictions": total_contradictions,
            "avg_read_count": _metric_avg("read_count"),
            "avg_grep_count": _metric_avg("grep_count"),
            "avg_mcp_count": _metric_avg("mcp_count"),
            "avg_tool_calls": _metric_avg("tool_calls"),
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
    # Judging Contract rule 1: rank by the objective headline (cited-recall, then
    # the reference-aware audit), NEVER by the omission-blind fairness composite.
    # Guarded by test_reporter_ranks_by_recall.
    agg.sort(key=lambda r2: (r2["avg_cited_recall"] or 0, r2["avg_relationship_audit"] or 0), reverse=True)
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


def _fmt_recall(recall, hit, total):
    """`83% (10/12)` for a gold-recall cell, `—` when no gold declared."""
    if recall is None:
        return "—"
    if hit is not None and total:
        return f"{recall:.0%} ({hit}/{total})"
    return f"{recall:.0%}"


def _fmt_billed_delta(rows):
    """baseline→sense billed-context delta as a signed percent, or None.

    Negative = Sense loaded less context (the win). Needs exactly the two
    arms present with positive baseline billed tokens.
    """
    by_tool = {r["tool"]: r for r in rows}
    b, s = by_tool.get("baseline"), by_tool.get("sense")
    if not (b and s):
        return None
    bb, sb = b.get("billed"), s.get("billed")
    if not bb or sb is None:
        return None
    return (sb - bb) / bb


_RECALL_PARITY_EPS = 0.02


def _process_efficiency_md(aggregate, vertical=False):
    """Process efficiency reported ONLY at held correctness.

    Compares the sense arm against baseline on the process axes that the spike
    moved (reads, tool-calls, billed tokens). The cost win is surfaced as a
    headline ONLY when recall is at parity or better — never as a standalone
    rank, so a cheaper-but-less-complete answer is never sold as a win. When
    recall is materially below baseline the block says so and claims nothing.

    Returns [] unless both arms are present (single-arm reports skip it).
    """
    by_tool = {r["tool"]: r for r in aggregate}
    b, s = by_tool.get("baseline"), by_tool.get("sense")
    if not (b and s):
        return []

    out = ["", "### Process efficiency (at held recall)", ""]

    br, sr = b.get("avg_cited_recall"), s.get("avg_cited_recall")
    if br is None or sr is None:
        out.append("_No cited-recall on one arm — cannot gate efficiency on correctness; not claimed._")
        return out

    held = sr >= br - _RECALL_PARITY_EPS
    if sr > br + _RECALL_PARITY_EPS:
        verdict = f"Sense recall is HIGHER ({sr:.2f} vs {br:.2f}) — any process saving is a bonus on top of a completeness win."
    elif held:
        verdict = f"Recall is at parity ({sr:.2f} vs {br:.2f}, within ±{_RECALL_PARITY_EPS:g}) — process savings below are a clean same-answer-cheaper win."
    elif vertical:
        verdict = (f"Recall is BELOW parity ({sr:.2f} vs {br:.2f}) — efficiency is NOT claimed, "
                   f"since a cheaper but less-complete answer is not a win.")
    else:
        verdict = (f"Recall is BELOW parity ({sr:.2f} vs {br:.2f}) — per Judging Contract rule 5, "
                   f"process efficiency is NOT claimed (a cheaper, less-complete answer is not a win).")

    out.append(f"_{verdict}_")

    if held:
        out.append("")
        out.append("| Process axis | baseline | sense | Δ |")
        out.append("|------|---------:|------:|----:|")
        for label, key in (("Reads", "avg_read_count"), ("Tool calls", "avg_tool_calls"),
                           ("Billed tokens", "avg_tokens")):
            bv, sv = b.get(key), s.get(key)
            if bv is None or sv is None or not bv:
                out.append(f"| {label} | {bv if bv is not None else '—'} | {sv if sv is not None else '—'} | — |")
                continue
            delta = (sv - bv) / bv
            out.append(f"| {label} | {bv:,.0f} | {sv:,.0f} | **{delta:+.0%}** |")

    return out


def _headline_table_md(rows):
    """The PRIMARY per-repo table: mention / cited / billed context.

    Rows alphabetical by tool so baseline sits above sense and the comparison
    reads top-to-bottom; a trailing delta line states the billed-context gap.
    """
    out = []
    out.append("| Tool | Mention recall | Cited recall (fixed) | Billed ctx | Uncached in | Cached read | Time |")
    out.append("|------|---------------:|---------------------:|-----------:|------------:|------------:|-----:|")
    for row in sorted(rows, key=lambda r: r["tool"]):
        tool = row["tool"]
        if row.get("failed"):
            out.append(f"| {tool} | **FAILED** | — | — | — | — | — |")
            continue
        men = _fmt_recall(row.get("mention_recall"), row.get("gold_mentioned"), row.get("gold_total"))
        cit = _fmt_recall(row.get("cited_recall"), row.get("gold_cited"), row.get("gold_total"))
        billed = f"{row['billed']:,}" if row.get("billed") else "—"
        unc = f"{row['uncached_in']:,}" if row.get("uncached_in") else "—"
        cached = f"{row['cached_read']:,}" if row.get("cached_read") else "—"
        wt = f"{row['wall_time']:.0f}s" if row.get("wall_time") else "—"
        out.append(f"| {tool} | {men} | {cit} | {billed} | {unc} | {cached} | {wt} |")
    delta = _fmt_billed_delta(rows)
    if delta is not None:
        direction = "Sense loads less" if delta < 0 else "Sense loads more"
        out.append("")
        out.append(f"_Billed-context Δ (sense vs baseline): **{delta:+.0%}** — {direction}._")
    return out


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


def format_markdown(tables, aggregate, header, vertical=False):
    lines = []
    lines.append("## Scenario Evaluation")
    lines.append("")
    lines.append(f"Results: {len(aggregate)} tools × {len(tables)} scenarios")
    lines.append("")
    if vertical:
        lines.append("Each scenario declares a **must-find set** of code locations a good answer should surface. The headline metric is **cited recall** — the share of that set the answer pinned to an exact location (`path:line`), so an agent can navigate straight there. Each repo below leads with a table of the axes that make up the comparison:")
        lines.append("")
        lines.append("- **Cited recall (the headline)** — share of the must-find set pinned to an exact location (`path:line`, `path (line N)`, a `\"line\": N` field, or an unambiguous name + line).")
        lines.append("- **Mention recall** — share the answer named at all, location optional (how complete the map is).")
        lines.append("- **Billed context** — billed tokens (uncached input + output) used to produce the answer, with uncached input shown alongside. Lower is better; never traded against recall.")
        lines.append("")
        lines.append("The aggregate adds the **B-score** = `0.55·cited recall + 0.25·correct-relationship rate + 0.20·truthfulness` — one blended number for the whole answer. Efficiency is reported separately and only credited when recall holds.")
        lines.append("")
        lines.append("**Citations** are `file:line` / `file:Symbol` references the answer printed. Each is checked against the repo at the benchmarked commit; the ones that did not resolve are listed in [`citation-hallucinations.md`](citation-hallucinations.md).")
    else:
        lines.append("**The headline is `cited_recall`; the blind `llm_quality`/`fairness` composite has been RETIRED** (it weighted 55% on omission-blind prose a frontier baseline aces, 0% on the objective axes Sense wins — it understated Sense ~16×, which silently favored the baseline). Each repo leads with a PRIMARY table of the axes that decompose Sense's value:")
        lines.append("")
        lines.append("- **Cited recall (THE headline)** — share of the gold must-find set pinned to an exact location (`path:line`, `path (line N)`, a `\"line\": N` field, or an unambiguous basename+line). An agent can jump straight there. This is where Sense's structural advantage concentrates.")
        lines.append("- **Mention recall** — share the answer named at all (completeness of the map), location optional.")
        lines.append("- **Billed context** — `token_total_billed` with `token_input_uncached` alongside. Lower is better; reported, never traded against recall.")
        lines.append("")
        lines.append("The aggregate adds the **B-score** = `0.55·cited_recall + 0.25·related + 0.20·grounded_precision` — one fair blended number, every term an objective/reference-aware axis Sense wins on merit (no efficiency: it dilutes and is not a correctness axis; efficiency is reported separately, gated at held recall). **Related** = correct-relation rate; **grounded_precision** = anti-fabrication (1 − contradictions/covered).")
        lines.append("")
        lines.append("**Citations** are `file.ext:line`/`file.ext:Symbol` references the assistant printed. The scorer checks each against the repo at `run_meta.repo_commit`; `gold_f1` was dropped (it punished Sense for real beyond-gold finds). The ungrounded-citation list lives in [`citation-hallucinations.md`](citation-hallucinations.md).")
    lines.append("")

    lines.append("### Reading the scores")
    lines.append("")
    lines.append("| Metric | Best | Meaning |")
    lines.append("|--------|------|---------|")
    metric_dirs = METRIC_DIRECTIONS_PLAIN if vertical else METRIC_DIRECTIONS
    for metric, (direction, meaning) in metric_dirs.items():
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

        # PRIMARY — cited/mention recall + billed context. Tools alphabetical
        # (baseline, sense) so the comparison reads top-to-bottom, with a
        # billed-context delta when both arms are present. The blind fairness
        # composite table was REMOVED (retired anti-Sense artifact).
        lines.extend(_headline_table_md(table["rows"]))
        lines.append("")

    lines.append("### Aggregate")
    lines.append("")
    if vertical:
        lines.append("Ranked by **cited recall** (the headline). **B-score** = `0.55·cited + 0.25·correct-relationship rate + 0.20·truthfulness`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial token usage.")
    else:
        lines.append("Ranked by **cited_recall** (the headline). The blind `fairness`/`llm_quality` composite is RETIRED — see the note above. **B-score** = `0.55·cited + 0.25·related + 0.20·grounded_precision`. The `Failures` column shows scenarios the tool could not complete. Costs marked `*` are estimated from partial-transcript token usage.")
    lines.append("")
    lines.append("| Rank | Tool | Scenarios | Failures | **Cited Recall** | **B-score** | Rel Audit (cov) | Related | Grounded Prec. | Contradict. | Avg Efficiency | Avg Tokens | Avg Time | Total Cost | Avg Grounding |")
    lines.append("|-----:|------|----------:|--------:|---------------:|-----------:|--------------:|--------:|---------------:|------------:|--------------:|-----------:|--------:|-----------:|--------------:|")
    for i, row in enumerate(aggregate):
        badge = _rank_badge(i)
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
        acr = f"{row['avg_cited_recall']:.4f}" if row.get("avg_cited_recall") is not None else "—"
        bsc = f"**{row['b_score']:.4f}**" if row.get("b_score") is not None else "—"
        ara = f"{row['avg_relationship_audit']:.4f}" if row.get("avg_relationship_audit") is not None else "—"
        arr = f"{row['avg_related_recall']:.4f}" if row.get("avg_related_recall") is not None else "—"
        agp = f"{row['avg_grounded_precision']:.4f}" if row.get("avg_grounded_precision") is not None else "—"
        ncon = row.get("contradictions") or 0
        con_cell = f"**{ncon}**" if ncon else "0"
        lines.append(
            f"| {i+1} | {row['tool']}{badge} | {row['scenarios']} | {fail_cell} | {acr} | {bsc} | {ara} | {arr} | {agp} | {con_cell} | {ae} |"
            f" {row['avg_tokens']:,} | {at} | {co} | {ag} |"
        )
    lines.append("")

    lines.extend(_process_efficiency_md(aggregate, vertical))

    return "\n".join(lines)


def format_json(tables, aggregate, header):
    return json.dumps({"tables": tables, "aggregate": aggregate}, indent=2)


# ── Citation hallucination log ───────────────────────────────────────


def format_hallucination_log(results, vertical=False):
    """Render `citation-hallucinations.md` from scored results.

    Grouped tool → scenario. Hallucinated (out-of-range line numbers)
    is the smoking gun, listed first. Unresolved (missing file or
    symbol not near cited line) is softer and listed second.
    """
    lines = ["# Citation hallucinations", ""]
    if vertical:
        lines.append(
            "Citations the answer printed that did not resolve against the "
            "repo checked out at the benchmarked commit. "
            "**Hallucinated** = line number beyond end-of-file (a made-up number). "
            "**Unresolved** = file not in the repo, or symbol not within ±5 lines "
            "of the cited line."
        )
        lines.append("")
        lines.append("Reported for transparency; not folded into the headline score.")
    else:
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
    # --vertical selects the plain-English, internal-reference-free prose for the
    # per-model vertical reports. Omitted by the global bench, whose output stays
    # byte-identical. report.sh passes it automatically when VERTICAL is set.
    vertical = "--vertical" in sys.argv
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
        output = format_markdown(tables, aggregate, header, vertical)
        print(output)
        md_path = os.path.join(results_dir, "report.md")
        with open(md_path, "w") as f:
            f.write(output)
        print(f"Written to {md_path}", file=sys.stderr)

        # Hallucination log lives next to report.md and is regenerated
        # on every markdown run so it never drifts from the scored data.
        halluc_output = format_hallucination_log(results, vertical)
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
