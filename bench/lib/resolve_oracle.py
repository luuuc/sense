#!/usr/bin/env python3
"""Tier-0 resolution oracle — deterministic, $0, no LLM, no scenario, no judge.

The isolated product unit-test the vertical bench is too coarse to be. For each
repo's CENTRAL contract symbol it runs the LIVE `sense` CLI against the repo's
pinned `.sense` index and asserts two things mechanically:

  COVERAGE — does `sense blast <symbol>` (default min_confidence 0.7, the agent's
    default) actually return the gold DISCRIMINATOR dependents the scenario says it
    must? Missing gold here is a LIVE-CONFIRMED resolver miss (stronger than the
    transcript_miss inference, which only saw what the agent happened to query).

  AMBIGUITY — does the naive `sense graph/blast <symbol>` (no --file) resolve, or
    does it error/empty while the --file form returns a real set? That is the exact
    gap transcript_miss surfaced (graph(Status)→0 vs graph(Status,file=)→111). When
    the graph-disambiguation fix lands, this flag clears — so the oracle doubles as
    the regression net for that fix.

This is the unit the piking loop gates on: change Sense → re-run the oracle → a
deterministic diff, for free, before any bench token is spent. It is a DETECTOR and
a REGRESSION NET, never the ship gate (the differential bench stays that). Exit code
is non-zero if any repo FAILs, so it can gate CI / the loop driver.

Usage:
  python3 bench/lib/resolve_oracle.py                  # all seeded repos
  python3 bench/lib/resolve_oracle.py --repo chatwoot,mastodon
  python3 bench/lib/resolve_oracle.py --json out.json
"""
import argparse
import glob
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(__file__))
import yaml  # noqa: E402

SENSE_REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))  # oss/sense
CLONES = os.path.join(os.path.dirname(SENSE_REPO), "sense-benchmark", "sense")     # per-repo clones + .sense
VERTICALS = os.path.join(SENSE_REPO, "bench", "verticals")  # verticals/<stack>/scenarios/
SENSE_BIN = os.environ.get("SENSE_BIN", os.path.expanduser("~/.local/bin/sense"))

ANCHOR_GROUPS = {"contract", "context", "surface", "teardown"}
COV_FAIL, COV_WARN = 0.50, 0.80   # blast coverage of the gold discriminator

# (repo) -> {symbol, file?}. The contract symbol the scenario puts under change.
# PREFERRED source is each gold yaml's top-level `contract_symbol:` (+ optional
# `contract_file:`) — stack-agnostic, so a new vertical (Django) is picked up with
# zero code edits. This hand-table is the rails-vintage FALLBACK for the frozen
# yamls that predate the field. `file` is set only for AMBIGUOUS symbols.
CONTRACTS = {
    # big apps — high confidence (the discriminator wins live here)
    "chatwoot":   {"symbol": "Inbox"},
    "discourse":  {"symbol": "Upload"},
    "forem":      {"symbol": "Article"},
    "mastodon":   {"symbol": "Status", "file": "app/models/status.rb"},   # ambiguous: Ruby model vs JS
    "gitlabhq":   {"symbol": "MergeRequest", "file": "app/models/merge_request.rb"},   # ambiguous
    "solidus":    {"symbol": "Spree::Order", "file": "core/app/models/spree/order.rb"},  # re-opened
    "rails":      {"symbol": "ActiveRecord::Relation", "file": "active_record/relation.rb"},  # re-opened
    "redmine":    {"symbol": "Issue"},
    "lobsters":   {"symbol": "Story"},
    # gems — best-guess symbols (adoption-gapped in the bench; fix the symbol if "not found")
    "ruby_llm":   {"symbol": "RubyLLM::Provider", "gem": True},
    "langchainrb": {"symbol": "Langchain::LLM::Base", "gem": True},
    "llm.rb":     {"symbol": "LLM::Provider", "gem": True},
    "raix":       {"symbol": "Raix::ChatCompletion", "gem": True},
}


def discover_contracts(stack):
    """Build {repo: {symbol, file?}}: gold yaml `contract_symbol:` wins (stack-agnostic,
    Django-ready). The rails-vintage CONTRACTS hand-table is the fallback for ruby-rails
    ONLY (its frozen yamls predate the field); other stacks rely on `contract_symbol:`."""
    out = dict(CONTRACTS) if stack == "ruby-rails" else {}
    for f in glob.glob(os.path.join(VERTICALS, stack, "scenarios", "*.yaml")):
        if ".rubric" in os.path.basename(f):
            continue
        repo = os.path.basename(f)[:-5]
        try:
            doc = yaml.safe_load(open(f)) or {}
        except yaml.YAMLError:
            continue
        sym = doc.get("contract_symbol")
        if sym:
            spec = {"symbol": sym}
            if doc.get("contract_file"):
                spec["file"] = doc["contract_file"]
            if doc.get("gem"):
                spec["gem"] = True
            out[repo] = spec  # yaml-declared overrides the hand-table
    return out


def run_sense(clone, args):
    """Run `sense <args>` in the clone. Returns (exit_code, parsed_json_or_None)."""
    try:
        p = subprocess.run([SENSE_BIN, *args], cwd=clone, capture_output=True,
                           text=True, timeout=120)
    except (subprocess.TimeoutExpired, FileNotFoundError) as e:
        return (-1, {"_error": str(e)})
    out = p.stdout.strip()
    if not out:
        return (p.returncode, None)
    try:
        return (p.returncode, json.loads(out))
    except json.JSONDecodeError:
        return (p.returncode, None)


def harvest_files(obj):
    """Recursively collect every value under a 'file' key (repo-relative paths)."""
    found = set()
    if isinstance(obj, dict):
        f = obj.get("file")
        if isinstance(f, str) and "." in f:
            found.add(f)
        for v in obj.values():
            found |= harvest_files(v)
    elif isinstance(obj, list):
        for v in obj:
            found |= harvest_files(v)
    return found


def discriminator_gold(stack, repo):
    """Expected files = the gold's discriminator group (largest non-anchor group)."""
    f = os.path.join(VERTICALS, stack, "scenarios", f"{repo}.yaml")
    if not os.path.exists(f):
        return set(), "?"
    doc = yaml.safe_load(open(f))
    by_group = {}
    for t in doc.get("gold", []):
        m = t.get("match")
        m = [m] if isinstance(m, str) else (m or [])
        by_group.setdefault(t.get("group", "?"), []).extend(m)
    disc = [g for g in by_group if g not in ANCHOR_GROUPS]
    if not disc:  # gem scenarios sometimes have no anchor split
        group = max(by_group, key=lambda g: len(by_group[g])) if by_group else "?"
    else:
        group = max(disc, key=lambda g: len(by_group[g]))
    return {p for p in by_group.get(group, [])}, group


def covered(expected, returned):
    """suffix-tolerant set coverage."""
    hit = set()
    for e in expected:
        if any(e == r or r.endswith("/" + e) or e.endswith("/" + r) for r in returned):
            hit.add(e)
    return hit


def check_repo(stack, repo, spec):
    clone = os.path.join(CLONES, repo)
    if not os.path.isdir(os.path.join(clone, ".sense")):
        return {"repo": repo, "status": "SKIP", "note": "no .sense index"}
    symbol = spec["symbol"]
    file_arg = spec.get("file")

    # 1) canonical blast (with --file if the symbol is ambiguous) → coverage
    blast_args = ["blast", symbol, "--json"]
    if file_arg:
        blast_args += ["--file", file_arg]
    bcode, bjson = run_sense(clone, blast_args)
    blast_files = harvest_files(bjson) if bjson else set()
    direct = len(bjson.get("direct_callers", [])) if isinstance(bjson, dict) else 0

    expected, group = discriminator_gold(stack, repo)

    # 2) uncapped graph edges (--direction both) = the RESOLUTION-truth signal.
    # blast's direct_callers are budget-capped (~60); graph called_by/composes/etc
    # are not — so coverage must be measured against blast ∪ graph, else the cap
    # gets misread as a resolver miss (two different product axes).
    gboth_args = ["graph", symbol, "--direction", "both", "--json"]
    if file_arg:
        gboth_args += ["--file", file_arg]
    _gc, gboth = run_sense(clone, gboth_args)
    graph_files = harvest_files(gboth) if gboth else set()

    resolved = blast_files | graph_files
    hit_resolved = covered(expected, resolved)        # does Sense resolve it AT ALL?
    hit_blast = covered(expected, blast_files)         # is it in the budgeted slice?
    cov = len(hit_resolved) / len(expected) if expected else None
    cov_blast = len(hit_blast) / len(expected) if expected else None
    missing = sorted(expected - hit_resolved)          # GENUINE resolver-miss candidates
    budget_evicted = sorted((hit_resolved - hit_blast)) # resolved but cap-evicted from blast

    # 3) ambiguity probe — naive graph (no --file) vs disambiguated
    gnaive_code, gnaive = run_sense(clone, ["graph", symbol, "--direction", "callers", "--json"])
    naive_cb = len(gnaive.get("edges", {}).get("called_by", [])) if isinstance(gnaive, dict) else 0
    if file_arg:
        gdis_code, gdis = run_sense(clone, ["graph", symbol, "--direction", "callers",
                                            "--file", file_arg, "--json"])
        dis_cb = len(gdis.get("edges", {}).get("called_by", [])) if isinstance(gdis, dict) else 0
    else:
        gdis_code, dis_cb = gnaive_code, naive_cb
    # AMBIGUOUS = naive errors/empties while a real set exists (disambiguated or blast)
    ambiguous = (naive_cb == 0) and (dis_cb > 0 or direct > 0)

    # verdict
    status = "PASS"
    flags = []
    if bcode == 2 and not file_arg:
        flags.append("BLAST_AMBIGUOUS")
    if symbol and direct == 0 and not blast_files:
        status, note = "FAIL", "symbol resolved to 0 callers/files (wrong symbol or resolver miss)"
        flags.append("EMPTY_BLAST")
    if ambiguous:
        flags.append("GRAPH_AMBIGUOUS_EMPTY")  # the transcript_miss finding, live-confirmed
    if budget_evicted:
        flags.append(f"BUDGET_EVICTED×{len(budget_evicted)}")  # resolved but capped out of blast
    if cov is not None:  # FAIL/WARN on RESOLVED coverage — a true resolver gap, not the cap
        if cov < COV_FAIL:
            status = "FAIL"
        elif cov < COV_WARN and status != "FAIL":
            status = "WARN"
    if ambiguous and status == "PASS":
        status = "WARN"

    return {
        "repo": repo, "symbol": symbol, "file": file_arg, "status": status,
        "gem": spec.get("gem", False), "group": group,
        "direct_callers": direct, "blast_files": len(blast_files),
        "coverage": round(cov, 3) if cov is not None else None,
        "coverage_blast_slice": round(cov_blast, 3) if cov_blast is not None else None,
        "expected": len(expected), "covered": len(hit_resolved),
        "missing_gold": missing, "budget_evicted": budget_evicted,
        "graph_naive_callers": naive_cb, "graph_disambig_callers": dis_cb,
        "flags": flags,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--stack", default="ruby-rails")
    ap.add_argument("--repo", default="", help="comma-separated repo filter")
    ap.add_argument("--json", default="")
    args = ap.parse_args()
    if not os.path.exists(SENSE_BIN):
        sys.exit(f"sense binary not found at {SENSE_BIN} (set SENSE_BIN=)")

    repo_filter = set(r.strip() for r in args.repo.split(",") if r.strip())
    results = []
    for repo, spec in discover_contracts(args.stack).items():
        if repo_filter and repo not in repo_filter:
            continue
        results.append(check_repo(args.stack, repo, spec))

    report(results, args.stack)
    if args.json:
        json.dump(results, open(args.json, "w"), indent=2)
        print(f"\nwrote {args.json}")
    sys.exit(1 if any(r["status"] == "FAIL" for r in results) else 0)


def report(results, stack):
    icon = {"PASS": "✅", "WARN": "🟡", "FAIL": "❌", "SKIP": "·"}
    order = {"FAIL": 0, "WARN": 1, "PASS": 2, "SKIP": 3}
    print(f"\n=== Tier-0 resolution oracle — stack={stack} "
          f"(live `sense` vs gold discriminator, $0) ===\n")
    for r in sorted(results, key=lambda x: order.get(x["status"], 9)):
        if r["status"] == "SKIP":
            print(f"{icon['SKIP']} {r['repo']:<12} SKIP — {r.get('note')}")
            continue
        cov = f"{r['coverage']*100:.0f}%" if r["coverage"] is not None else "n/a"
        covb = f"{r['coverage_blast_slice']*100:.0f}%" if r.get("coverage_blast_slice") is not None else "n/a"
        gem = " (gem)" if r["gem"] else ""
        print(f"{icon[r['status']]} {r['repo']:<12}{gem}  {r['symbol']}"
              + (f"  [file:{r['file']}]" if r['file'] else ""))
        print(f"     resolved coverage {cov} ({r['covered']}/{r['expected']} gold '{r['group']}') | "
              f"blast budgeted-slice {covb} ({r['direct_callers']} direct) | "
              f"graph callers naive={r['graph_naive_callers']} disambig={r['graph_disambig_callers']}")
        if r["flags"]:
            print(f"     ⚑ {', '.join(r['flags'])}")
        if r["missing_gold"]:
            show = r["missing_gold"][:6]
            more = "" if len(r["missing_gold"]) <= 6 else f"  (+{len(r['missing_gold'])-6} more)"
            print(f"     UNRESOLVED gold (true resolver-miss candidates): {', '.join(show)}{more}")
        if r["budget_evicted"]:
            show = r["budget_evicted"][:4]
            more = "" if len(r["budget_evicted"]) <= 4 else f"  (+{len(r['budget_evicted'])-4} more)"
            print(f"     budget-evicted (resolved, but capped out of blast): {', '.join(show)}{more}")
        print()
    n = {k: sum(1 for r in results if r["status"] == k) for k in icon}
    print(f"summary: {n['PASS']} pass · {n['WARN']} warn · {n['FAIL']} fail · {n['SKIP']} skip")
    print("Tier-0 is a DETECTOR + REGRESSION NET. A FAIL/WARN is a fix candidate, not a verdict; "
          "ship only behind a differential bench. GRAPH_AMBIGUOUS_EMPTY clears when graph mirrors "
          "blast's disambiguation.")


if __name__ == "__main__":
    main()
