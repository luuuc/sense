#!/usr/bin/env python3
"""Run the reference-aware relationship audit on EXISTING transcripts ($0, no re-bench).

The chatwoot $0 re-score path: after adding `relation:` fields to a scenario's gold,
grade the already-run transcripts against that fixed reference (covered/related per
target) without spending a new bench. Uses judge.py's CLI judge.

  BENCH_JUDGE_VIA_CLI=1 python3 bench/lib/rescore_audit.py <repo>

Reads bench/scenarios/<repo>.yaml (for gold + relations) and
bench/results/{baseline,sense}/<repo>/run-*/transcript.json. Prints covered/related
per arm/run. CAVEAT: this LLM judge OVER-CREDITS homogeneous fan-outs (it credits a
specific instance from a generic family/glob mention) — treat as SECONDARY; the
headline is objective per-group cited-recall (pergroup.py).
"""
import json, os, sys, glob

LIB = os.path.dirname(__file__)
sys.path.insert(0, LIB)
import yaml
from judge import call_judge, extract_judge_json
from relationship_audit import grade

REPO = sys.argv[1] if len(sys.argv) > 1 else sys.exit("usage: rescore_audit.py <repo>")
scenario = yaml.safe_load(open(os.path.join(LIB, "..", "scenarios", f"{REPO}.yaml")))
gold = scenario.get("gold")
subject = scenario.get("name", "the contract under change")


def extract_answer(path):
    txt = ""
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                o = json.loads(line)
            except Exception:
                continue
            if o.get("type") == "result" and "result" in o:
                txt = o["result"]
            elif o.get("type") == "assistant":
                for c in o.get("message", {}).get("content", []):
                    if c.get("type") == "text":
                        txt = c["text"]
    return txt


for arm in ("baseline", "sense"):
    base = os.path.join(LIB, "..", "results", arm, REPO)
    paths = sorted(glob.glob(os.path.join(base, "run-*", "transcript.json"))) or \
        ([os.path.join(base, "transcript.json")] if os.path.exists(os.path.join(base, "transcript.json")) else [])
    for p in paths:
        run = os.path.basename(os.path.dirname(p))
        ans = extract_answer(p)
        res = grade(ans, gold, call_judge=call_judge, extract_json=extract_judge_json,
                    subject=subject, api_key="")
        if res is None:
            print(f"{arm}/{run}: gold has no relation fields — add them first")
            continue
        print(f"{arm}/{run}: covered={res['covered_recall']} related={res['related_recall']} "
              f"| missed_related={res['missed_related']}")
