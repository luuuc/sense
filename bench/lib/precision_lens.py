#!/usr/bin/env python3
"""Precision lens: per-arm citation PRECISION for a call-site audit scenario.

pergroup.py / gold.py measure RECALL against a curated must-find set; the LLM
judge is blind to omission AND over-collection. Neither can see the precision
axis: a baseline that greps a noisy method token (`.get(`) and mis-judges
receivers emits FALSE POSITIVES (dict.get / cache.get cited as ORM call
sites), and no existing metric charges it for them. This lens measures that.

For one run's answer it extracts every `file:line` citation, keeps the ones
that CLAIM a call site of the target method (the cited line actually contains
the method token; `def <method>` definition lines are excluded), and buckets
each claim:

  true_by_index   — the bench-time Sense index holds a resolved
                    `<Class>.<method>` call edge at that file:line (± a small
                    tolerance for multi-line calls). The index's convention-
                    typed edges are spot-checked precise, so an index match
                    CONFIRMS a claim.
  adjudicated     — a human/AI verdict recorded in the shared adjudication
                    file (true or false, with a note). This is the honesty
                    guard: the index under-resolves (unannotated queryset
                    locals), so a claim it cannot confirm is NEVER auto-false
                    — a baseline citing a real ORM call site Sense missed must
                    not be penalised by Sense's own recall gap.
  unadjudicated   — needs a verdict; printed with the source snippet so
                    adjudication is fast and auditable.

Precision is reported as a floor/ceiling pair until every claim is
adjudicated (floor: unknowns false; ceiling: unknowns true); with zero
unknowns the two collapse to the final number. The gold cited-recall of the
scenario (the recall FLOOR the headline must hold) is printed alongside from
gold.py so one invocation shows both axes.

The adjudication file is keyed by resolved `path:line`, shared across arms
and runs — one verdict per site, both arms scored with the same truth.

Usage:
  precision_lens.py <result_dir> <scenario.yaml> <bench_dir> \
      --index <index.db> --repo <checkout> --cls QuerySet --method get \
      [--tolerance 3] [--adjudication <verdicts.json>]

Writes precision.json into result_dir and prints the human summary.
"""

import argparse
import json
import os
import re
import sqlite3
import sys


# ── Citation extraction (three pin forms agents actually emit) ───────


_JSON_PIN_RE = re.compile(
    r'"(?:file|path)"\s*:\s*"([^"]+\.py)"[^{}]{0,80}?"line"\s*:\s*"?(\d+)')
_PAREN_PIN_RE = re.compile(r'([\w/\-.]+\.py)\s*\(line\s+(\d+)\)', re.IGNORECASE)


def extract_pins(answer_text):
    """(file, line) pairs from path:N, JSON-object, and parenthetical forms."""
    from grounding import extract_citations, _classify_locator
    pins = []
    for file, locator in extract_citations(answer_text):
        line, _sym = _classify_locator(locator)
        if line is not None:
            pins.append((file, line))
    for regex in (_JSON_PIN_RE, _PAREN_PIN_RE):
        for file, line in regex.findall(answer_text):
            pins.append((file, int(line)))
    seen, out = set(), []
    for p in pins:
        if p not in seen:
            seen.add(p)
            out.append(p)
    return out


# ── Claim detection ──────────────────────────────────────────────────


def read_lines(repo, rel):
    try:
        with open(os.path.join(repo, rel), encoding="utf-8", errors="replace") as f:
            return f.readlines()
    except OSError:
        return None


def claim_kind(lines, line, method):
    """How strongly does the cited line claim a call of `method`?

    "exact" — the cited line itself contains `method(` (not a `def`).
    "near"  — only a neighbour (±2, a multi-line call cited at its statement
              start) does; not auto-decided, routed to adjudication.
    None    — no token in reach: not a call-site claim.
    """
    call = re.compile(r"\b" + re.escape(method) + r"\(")
    defn = re.compile(r"\bdef\s+" + re.escape(method) + r"\(")

    def hits(n):
        return 1 <= n <= len(lines) and call.search(lines[n - 1]) \
            and not defn.search(lines[n - 1])

    if hits(line):
        return "exact"
    if any(hits(n) for n in (line - 2, line - 1, line + 1, line + 2)):
        return "near"
    return None


# ── Index truth ──────────────────────────────────────────────────────


def load_index_edges(index_db, cls, method):
    """{file_path: [(line, confidence)]} for every resolved <cls>.<method>
    call edge in the bench-time index (all confidences; the caller records
    the matched edge's confidence so a 0.3-tier match is visible)."""
    con = sqlite3.connect(index_db)
    rows = con.execute(
        """SELECT f.path, e.line, e.confidence
           FROM sense_edges e
           JOIN sense_symbols tgt ON tgt.id = e.target_id
           JOIN sense_symbols par ON par.id = tgt.parent_id
           JOIN sense_files f ON f.id = e.file_id
           WHERE e.kind = 'calls' AND tgt.name = ? AND par.name = ?""",
        (method, cls)).fetchall()
    con.close()
    edges = {}
    for path, line, conf in rows:
        edges.setdefault(path, []).append((line, conf))
    return edges


def index_match(edges, rel, line, tolerance):
    """Highest-confidence edge within tolerance of the cited line, or None."""
    best = None
    for eline, conf in edges.get(rel, []):
        if eline is not None and abs(eline - line) <= tolerance:
            if best is None or conf > best[1]:
                best = (eline, conf)
    return best


# ── Adjudication file ────────────────────────────────────────────────


def load_adjudications(path):
    if not path or not os.path.exists(path):
        return {}
    with open(path) as f:
        return json.load(f).get("adjudications", {})


# ── Lens ─────────────────────────────────────────────────────────────


def run_lens(answer_text, repo, edges, method, tolerance, adjudications):
    from grounding import _resolve_path

    claims, non_claims, unresolved = [], [], []
    seen_sites = set()
    for file, line in extract_pins(answer_text):
        rel, _note = _resolve_path(file, str(line), repo)
        if rel is None:
            unresolved.append(f"{file}:{line}")
            continue
        site = f"{rel}:{line}"
        if site in seen_sites:
            continue
        seen_sites.add(site)
        lines = read_lines(repo, rel)
        if lines is None or line > len(lines):
            non_claims.append(site)
            continue
        kind = claim_kind(lines, line, method)
        matched = index_match(edges, rel, line, tolerance)
        if kind is None and not matched:
            non_claims.append(site)
            continue
        snippet = lines[line - 1].strip()[:120]
        if matched:
            claims.append({"site": site, "bucket": "true_by_index",
                           "edge_confidence": matched[1], "snippet": snippet})
        elif site in adjudications:
            v = adjudications[site]
            bucket = "adjudicated_true" if v.get("verdict") else "adjudicated_false"
            claims.append({"site": site, "bucket": bucket,
                           "note": v.get("note", ""), "snippet": snippet})
        else:
            claims.append({"site": site, "bucket": "unadjudicated",
                           "claim_kind": kind, "snippet": snippet})

    n = len(claims)
    true_n = sum(1 for c in claims if c["bucket"] in ("true_by_index", "adjudicated_true"))
    false_n = sum(1 for c in claims if c["bucket"] == "adjudicated_false")
    unknown_n = n - true_n - false_n
    return {
        "claims": n,
        "true": true_n,
        "false": false_n,
        "unadjudicated": unknown_n,
        "precision_floor": round(true_n / n, 4) if n else None,
        "precision_ceiling": round((true_n + unknown_n) / n, 4) if n else None,
        "final": unknown_n == 0,
        "non_claim_citations": len(non_claims),
        "unresolved_citations": unresolved,
        "details": claims,
    }


def gold_floor(answer_text, scenario):
    from gold import score_gold_recall
    recall = score_gold_recall(answer_text, scenario.get("gold"))
    if not recall:
        return None
    return {"cited_recall": recall["cited_recall"],
            "groups": {g: v["cited_recall"] for g, v in recall["groups"].items()},
            "missed_cite": recall["missed_cite"]}


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("result_dir")
    ap.add_argument("scenario_yaml")
    ap.add_argument("bench_dir")
    ap.add_argument("--index", required=True, help=".sense/index.db of the bench-time index")
    ap.add_argument("--repo", required=True, help="repo checkout the citations refer to")
    ap.add_argument("--cls", required=True, help="contract class, e.g. QuerySet")
    ap.add_argument("--method", required=True, help="contract method, e.g. get")
    ap.add_argument("--tolerance", type=int, default=3,
                    help="max |cited line - edge line| still confirming (multi-line calls)")
    ap.add_argument("--adjudication", help="shared verdicts JSON (site -> {verdict, note})")
    args = ap.parse_args()

    sys.path.insert(0, os.path.join(args.bench_dir, "lib"))
    from scenario import parse as parse_scenario
    from scorer import read_transcript_texts

    scenario = parse_scenario(args.scenario_yaml)
    transcript = os.path.join(args.result_dir, "transcript.json")
    if not os.path.exists(transcript):
        print(json.dumps({"error": "transcript.json not found"}))
        sys.exit(1)
    answer_text, _audit = read_transcript_texts(transcript)

    edges = load_index_edges(args.index, args.cls, args.method)
    result = {
        "scenario": scenario["name"],
        "contract": f"{args.cls}.{args.method}",
        "precision": run_lens(answer_text, args.repo, edges, args.method,
                              args.tolerance, load_adjudications(args.adjudication)),
        "recall_floor": gold_floor(answer_text, scenario),
    }

    out = os.path.join(args.result_dir, "precision.json")
    with open(out, "w") as f:
        json.dump(result, f, indent=2)
        f.write("\n")

    p = result["precision"]
    print(f"contract {result['contract']}: {p['claims']} call-site claims — "
          f"{p['true']} true, {p['false']} false, {p['unadjudicated']} unadjudicated "
          f"(precision {p['precision_floor']}..{p['precision_ceiling']}"
          f"{', FINAL' if p['final'] else ''})", file=sys.stderr)
    for c in p["details"]:
        if c["bucket"] == "unadjudicated":
            print(f"  ADJUDICATE {c['site']}  |  {c['snippet']}", file=sys.stderr)
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
