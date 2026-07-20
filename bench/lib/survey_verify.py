#!/usr/bin/env python3
"""Post-run agent survey: parse, transcript-verify, and aggregate ($0, read-only).

session-run.sh appends one extra --resume turn to the sense arm AFTER the scored
transcript is closed (survey output goes to survey.json, never transcript.json,
so scoring/judge inputs are untouched). The agent answers five evidence-citing
questions about Sense, then self-scores 0-10 on an anchored band scale
(bench/lib/survey_prompt.md).

An agent self-report is a hypothesis, not a finding. This script is the killer
step: every cited instance is checked against transcript.json: did that Sense
call actually happen, did the fallback grep/read actually follow it, did the
query actually change after the hint? Instances that don't verify are KEPT and
marked confabulated (confabulation rate per model is itself signal).

Usage:
  python3 bench/lib/survey_verify.py --run <results/sense/<repo> dir> \
      [--append <surveys.jsonl>]            # parse+verify one run, emit record
  python3 bench/lib/survey_verify.py --report   # aggregate all surveys.jsonl
"""
import argparse
import glob
import json
import os
import re
import sys
import time

SENSE_REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
VERTICALS = os.path.join(SENSE_REPO, "bench", "verticals")

SENSE_TOOLS = ("sense_blast", "sense_graph", "sense_search", "sense_conventions", "sense_status")
FALLBACK_TOOLS = ("Grep", "Glob", "Read")
FALLBACK_BASH_RE = re.compile(r"\b(grep|rg|ag|find|cat|head|sed|awk)\b")


def _rows(path):
    out = []
    with open(path) as fh:
        for line in fh:
            line = line.strip()
            if line:
                try:
                    out.append(json.loads(line))
                except json.JSONDecodeError:
                    pass
    return out


def _events(rows):
    """Ordered tool events: (index, kind, name, input_str) plus sense result text."""
    events = []
    for r in rows:
        msg = r.get("message", {})
        if not isinstance(msg, dict):
            continue
        content = msg.get("content")
        if not isinstance(content, list):
            continue
        for blk in content:
            if not isinstance(blk, dict):
                continue
            if blk.get("type") == "tool_use":
                name = blk.get("name", "")
                inp = json.dumps(blk.get("input", {}), ensure_ascii=False)
                events.append({"i": len(events), "kind": "tool_use", "name": name, "input": inp})
            elif blk.get("type") == "tool_result":
                c = blk.get("content")
                if isinstance(c, list):
                    text = " ".join(x.get("text", "") for x in c if isinstance(x, dict))
                else:
                    text = c if isinstance(c, str) else ""
                events.append({"i": len(events), "kind": "tool_result", "name": "", "input": text})
    return events


def _final_text(rows):
    """The survey turn's final answer: prefer the stream-json result event."""
    for r in reversed(rows):
        if r.get("type") == "result" and isinstance(r.get("result"), str):
            return r["result"]
    texts = []
    for r in rows:
        msg = r.get("message", {})
        if r.get("type") == "assistant" and isinstance(msg, dict):
            for blk in msg.get("content") or []:
                if isinstance(blk, dict) and blk.get("type") == "text":
                    texts.append(blk.get("text", ""))
    return texts[-1] if texts else ""


def parse_answer(text):
    """Extract the survey JSON object from the agent's reply (fenced or bare)."""
    if not text:
        return None
    for cand in (text, text[text.find("{"): text.rfind("}") + 1]):
        try:
            obj = json.loads(cand)
            if isinstance(obj, dict) and "score" in obj:
                return obj
        except (json.JSONDecodeError, ValueError):
            continue
    return None


def _sense_calls(events):
    calls = []
    for e in events:
        if e["kind"] != "tool_use":
            continue
        base = e["name"].rsplit("__", 1)[-1]
        if base in SENSE_TOOLS:
            calls.append(e)
        elif e["name"] == "Bash" and re.search(r"\bsense\s+(blast|graph|search|conventions)\b", e["input"]):
            calls.append(e)
    return calls


def _match_call(calls, tool, query):
    """Sense calls matching the cited tool whose input contains the cited query."""
    tool = (tool or "").rsplit("__", 1)[-1].strip().lower()
    query = (query or "").strip()
    if not tool or not query:
        return []
    return [c for c in calls
            if tool in c["name"].lower() or (c["name"] == "Bash" and tool.replace("sense_", "") in c["input"])
            if query.lower() in c["input"].lower()]


def _has_fallback_after(events, idx):
    for e in events:
        if e["i"] <= idx or e["kind"] != "tool_use":
            continue
        if e["name"] in FALLBACK_TOOLS:
            return True
        if e["name"] == "Bash" and FALLBACK_BASH_RE.search(e["input"]):
            return True
    return False


def verify(answer, events):
    """Stamp verified/confabulated on every cited instance, in place."""
    calls = _sense_calls(events)
    total = ok = 0

    def stamp(inst, verified):
        nonlocal total, ok
        inst["verified"] = bool(verified)
        total += 1
        ok += bool(verified)

    for q in ("q1_accurate", "q1_wrong"):
        for inst in _insts(answer, q):
            stamp(inst, _match_call(calls, inst.get("tool"), inst.get("query")))
    for inst in _insts(answer, "q2_fallbacks"):
        hits = _match_call(calls, inst.get("tool"), inst.get("query"))
        stamp(inst, any(_has_fallback_after(events, c["i"]) for c in hits))
    for inst in _insts(answer, "q3_hints"):
        before = _match_call(calls, inst.get("tool"), inst.get("query_before"))
        after = [c for c in calls if (inst.get("query_after") or "").strip().lower() in c["input"].lower()]
        ordered = any(b["i"] < a["i"] for b in before for a in after)
        hint = (inst.get("hint") or "").strip().lower()
        inst["hint_found"] = bool(hint) and any(
            hint[:40] in e["input"].lower() for e in events if e["kind"] == "tool_result")
        stamp(inst, ordered)
    return {"instances": total, "verified": ok, "confabulated": total - ok}


def _insts(answer, key):
    v = answer.get(key)
    return [i for i in v if isinstance(i, dict)] if isinstance(v, list) else []


def build_record(run_dir, meta_extra=None):
    survey = os.path.join(run_dir, "survey.json")
    transcript = os.path.join(run_dir, "transcript.json")
    if not os.path.isfile(survey):
        return None, "no survey.json"
    answer = parse_answer(_final_text(_rows(survey)))
    if answer is None:
        return None, "survey answer is not parseable JSON"
    events = _events(_rows(transcript)) if os.path.isfile(transcript) else []
    counts = verify(answer, events)
    meta = {}
    meta_path = os.path.join(run_dir, "run_meta.json")
    if os.path.isfile(meta_path):
        with open(meta_path) as fh:
            meta = json.load(fh)
    rec = {
        "ts": time.strftime("%Y-%m-%dT%H:%M:%S"),
        "vertical": (meta_extra or {}).get("vertical") or "",
        "repo": meta.get("repo"), "scenario": meta.get("scenario"),
        "model": meta.get("model"), "tool_version": meta.get("tool_version"),
        "repo_commit": meta.get("repo_commit"),
        "score": answer.get("score"), "score_rationale": answer.get("score_rationale"),
        "q4_value": answer.get("q4_value"), "q5_improve": answer.get("q5_improve"),
        "answers": {k: answer.get(k) for k in ("q1_accurate", "q1_wrong", "q2_fallbacks", "q3_hints")},
        "verify": counts,
    }
    return rec, None


def report():
    paths = glob.glob(os.path.join(VERTICALS, "*", "results", "**", "surveys.jsonl"), recursive=True)
    recs = [r for p in sorted(set(paths)) for r in _rows(p)]
    if not recs:
        print("no surveys.jsonl found under bench/verticals/*/results/")
        return
    by_model = {}
    for r in recs:
        by_model.setdefault(r.get("model") or "?", []).append(r)
    print(f"== agent surveys: {len(recs)} runs ==")
    for model, rows in sorted(by_model.items()):
        scores = [r["score"] for r in rows if isinstance(r.get("score"), (int, float))]
        v = sum(r.get("verify", {}).get("verified", 0) for r in rows)
        t = sum(r.get("verify", {}).get("instances", 0) for r in rows)
        avg = f"{sum(scores) / len(scores):.1f}" if scores else "-"
        confab = f"{(t - v) / t:.0%}" if t else "-"
        print(f"  {model}: n={len(rows)} avg_score={avg} confab_rate={confab} ({t - v}/{t} instances)")
    print("\n== q5_improve (verified runs only advance to backlog; n-killer: need >=2 independent runs) ==")
    for r in recs:
        if r.get("q5_improve"):
            print(f"  [{r.get('model')}/{r.get('repo')}] {r['q5_improve']}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", help="results/sense/<repo> dir with survey.json + transcript.json")
    ap.add_argument("--append", help="surveys.jsonl to append the verified record to")
    ap.add_argument("--vertical", default=os.environ.get("VERTICAL", ""))
    ap.add_argument("--report", action="store_true")
    args = ap.parse_args()
    if args.report:
        report()
        return
    if not args.run:
        ap.error("--run or --report required")
    rec, err = build_record(args.run, {"vertical": args.vertical})
    if err:
        print(f"[survey] SKIP {args.run}: {err}", file=sys.stderr)
        sys.exit(1)
    line = json.dumps(rec, ensure_ascii=False)
    if args.append:
        with open(args.append, "a") as fh:
            fh.write(line + "\n")
        v = rec["verify"]
        print(f"[survey] {rec['repo']} score={rec['score']} "
              f"verified {v['verified']}/{v['instances']} -> {args.append}", file=sys.stderr)
    else:
        print(line)


if __name__ == "__main__":
    main()
