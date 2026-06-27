#!/usr/bin/env python3
"""Retroactive Sense-miss miner over already-paid bench transcripts ($0, read-only).

The vertical bench is a value-PROOF instrument (Loop B). It is the wrong tool for
DETECTING Sense product gaps (Loop A) — too slow, too noisy, too many tokens. But
every sense-arm run already recorded, for free, exactly what we need to detect gaps:
each `sense_blast`/`graph`/`search` call AND its full JSON response, then what the
agent did next (Reads, greps) and which gold targets it ultimately cited.

This script mines that corpus for three deterministic signals, no LLM, no re-bench:

  1. CITED-NOT-RETURNED (resolver miss, highest value) — a gold file the sense arm
     got credit for citing, that NONE of its Sense calls actually returned. Sense
     got the answer DESPITE its own tool (via a read/grep). blast/graph should have
     surfaced it. (This is the class that produced the acts_as + determinism fixes.)
  2. FALLBACK-READS (coverage/trust gap) — files the agent Read that no Sense call
     returned. High volume = Sense under-covered, the agent had to go find it itself.
     (The chatwoot "1 blast then 18 Reads" signature.)
  3. DEGENERATE-RETURN (resolution/ranking gap) — a blast/graph call that came back
     empty or near-empty, plus cross-run NONDETERMINISM on the same symbol (the exact
     signature of the high-fan-out blast bug we fixed once by hand).

Output is a ranked gap list to react to BEFORE spending a single new bench token.

Usage:
  python3 bench/lib/transcript_miss.py                 # whole rails corpus, opus-4-8
  python3 bench/lib/transcript_miss.py --model all      # every model dir
  python3 bench/lib/transcript_miss.py --repo chatwoot,discourse
  python3 bench/lib/transcript_miss.py --json out.json  # machine-readable dump
"""
import argparse
import collections
import glob
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(__file__))
import yaml  # noqa: E402

SENSE_REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))  # oss/sense
VERTICALS = os.path.join(SENSE_REPO, "bench", "verticals")  # verticals/<stack>/{results,scenarios}/

SENSE_TOOLS = ("sense_blast", "sense_graph", "sense_search")
# A gold group is a "discriminator" (the headline) unless it is one of the
# anchor groups both arms are expected to get. cited-not-returned in a
# discriminator group is the high-value product signal.
ANCHOR_GROUPS = {"contract", "context", "surface", "teardown"}

PATH_RE = re.compile(r'"(?:file|ref|path)"\s*:\s*"([^"]+?\.[a-zA-Z]{1,4})(?::\d+)?"')


def _load_jsonl(path):
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


def _blocks(rows):
    """Yield every content block in transcript order."""
    for r in rows:
        msg = r.get("message", {})
        if not isinstance(msg, dict):
            continue
        for b in msg.get("content") or []:
            if isinstance(b, dict):
                yield b


def _result_text(block):
    c = block.get("content")
    if isinstance(c, str):
        return c
    if isinstance(c, list):
        return " ".join(
            (p.get("text", "") if isinstance(p, dict) else str(p)) for p in c
        )
    return json.dumps(c)


def _rel(path, repo):
    """Strip the clone prefix .../<repo>/ from an absolute Read path."""
    marker = f"/{repo}/"
    i = path.rfind(marker)
    if i >= 0:
        return path[i + len(marker):]
    return path.lstrip("/")


def analyze_run(run_dir, repo, gold_by_id):
    tpath = os.path.join(run_dir, "transcript.json")
    spath = os.path.join(run_dir, "scored.json")
    if not os.path.exists(tpath):
        return None
    rows = _load_jsonl(tpath)

    # Pair tool_use ids -> name/input; collect tool_results by id.
    uses = {}
    for b in _blocks(rows):
        if b.get("type") == "tool_use":
            uses[b.get("id")] = (b.get("name") or "", b.get("input") or {})

    sense_calls = []          # [(short_tool, input, returned_files, n_returned)]
    sense_files = set()       # every repo-rel path any Sense call returned
    read_files = []           # repo-rel paths the agent Read
    grep_calls = 0            # Bash greps/finds (text-search fallback)
    structural_calls = 0      # blast/graph calls (the resolvers, not search/status)

    for b in _blocks(rows):
        if b.get("type") == "tool_use":
            name, inp = b.get("name") or "", b.get("input") or {}
            if name == "Read":
                fp = inp.get("file_path") or ""
                if fp:
                    read_files.append(_rel(fp, repo))
            elif name == "Bash":
                cmd = (inp.get("command") or "")
                if re.search(r"\b(grep|rg|find|ag|ack)\b", cmd):
                    grep_calls += 1
        elif b.get("type") == "tool_result":
            tid = b.get("tool_use_id")
            name, inp = uses.get(tid, ("", {}))
            short = next((t for t in SENSE_TOOLS if t in name), None)
            if not short:
                continue
            txt = _result_text(b)
            files = set(m.group(1) for m in PATH_RE.finditer(txt))
            # the symbol's own definition file isn't a "returned dependent"
            files.discard(_symbol_self_file(inp, txt))
            sense_calls.append((short, inp, files, len(files)))
            sense_files |= files
            if short in ("sense_blast", "sense_graph"):
                structural_calls += 1

    # scored.json: which gold ids the arm CITED, and the per-id group.
    cited_ids = set()
    if os.path.exists(spath):
        try:
            sc = json.load(open(spath))
            for d in sc.get("gold_recall", {}).get("details", []):
                if d.get("cited"):
                    cited_ids.add(d.get("id"))
        except (json.JSONDecodeError, KeyError):
            pass

    # SIGNAL 1: cited-not-returned (per cited gold id whose files Sense never returned)
    cited_not_returned = []
    for gid in cited_ids:
        g = gold_by_id.get(gid)
        if not g:
            continue
        matches = g["match"]
        if not any(any(mf == sf or sf.endswith("/" + mf) or mf.endswith("/" + sf)
                       for sf in sense_files) for mf in matches):
            cited_not_returned.append(gid)

    # SIGNAL 2: fallback reads (Read files no Sense call returned)
    fallback_reads = [f for f in read_files if f not in sense_files
                      and not any(f.endswith("/" + sf) or sf.endswith("/" + f)
                                  for sf in sense_files)]
    reread_sense = [f for f in read_files if f not in fallback_reads]

    # SIGNAL 3: degenerate sense returns
    degenerate = [(s, _symbol_of(inp), n) for (s, inp, files, n) in sense_calls
                  if s in ("sense_blast", "sense_graph") and n == 0]

    return {
        "run": os.path.basename(run_dir),
        "sense_call_count": len(sense_calls),
        "structural_calls": structural_calls,   # blast/graph only
        "sense_calls": [(s, _symbol_of(inp), n) for (s, inp, _f, n) in sense_calls],
        "sense_files": sorted(sense_files),
        "n_read": len(read_files),
        "grep_calls": grep_calls,
        # a cited-not-returned id is a RESOLVER-miss candidate only when the run
        # actually exercised a structural resolver; otherwise it's an ADOPTION gap.
        "cited_not_returned": cited_not_returned if structural_calls else [],
        "adoption_gap": cited_not_returned if not structural_calls else [],
        "fallback_reads": fallback_reads,
        "reread_sense": reread_sense,
        "degenerate": degenerate,
    }


def _symbol_of(inp):
    return inp.get("symbol") or inp.get("query") or ""


def _symbol_self_file(inp, txt):
    # the queried symbol's own "file" appears once near the top of graph/blast output
    m = re.search(r'"symbol"\s*:\s*\{[^}]*?"file"\s*:\s*"([^"]+)"', txt)
    return m.group(1) if m else ""


def load_gold(stack, repo):
    f = os.path.join(VERTICALS, stack, "scenarios", f"{repo}.yaml")
    if not os.path.exists(f):
        return {}
    doc = yaml.safe_load(open(f))
    out = {}
    for t in doc.get("gold", []):
        matches = t.get("match")
        if isinstance(matches, str):
            matches = [matches]
        out[t["id"]] = {
            "group": t.get("group", "?"),
            "match": matches or [],
            "relation": t.get("relation", ""),
        }
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--stack", default="ruby-rails")
    ap.add_argument("--model", default="claude-opus-4-8",
                    help="model dir under verticals/<stack>/results/, or 'all'")
    ap.add_argument("--repo", default="", help="comma-separated repo filter")
    ap.add_argument("--json", default="", help="write machine-readable dump here")
    args = ap.parse_args()

    stack_dir = os.path.join(VERTICALS, args.stack, "results")
    if args.model == "all":
        model_dirs = [d for d in glob.glob(os.path.join(stack_dir, "*")) if os.path.isdir(d)]
    else:
        model_dirs = [os.path.join(stack_dir, args.model)]
    repo_filter = set(r.strip() for r in args.repo.split(",") if r.strip())

    # repo -> aggregated signal
    agg = {}
    gold_cache = {}
    n_runs = 0
    for md in model_dirs:
        sense_root = os.path.join(md, "sense")
        if not os.path.isdir(sense_root):
            continue
        for repo_dir in sorted(glob.glob(os.path.join(sense_root, "*"))):
            repo = os.path.basename(repo_dir)
            if repo_filter and repo not in repo_filter:
                continue
            gold = gold_cache.setdefault(repo, load_gold(args.stack, repo))
            for run_dir in sorted(glob.glob(os.path.join(repo_dir, "run-*"))):
                res = analyze_run(run_dir, repo, gold)
                if res is None:
                    continue
                n_runs += 1
                a = agg.setdefault(repo, {
                    "runs": 0, "cnr": collections.Counter(), "adopt_gap": collections.Counter(),
                    "fallback": collections.Counter(), "degenerate": collections.Counter(),
                    "grep_runs": 0, "noadopt_runs": 0, "sense_calls": 0, "reads": 0,
                    "sense_symbols": collections.defaultdict(set),
                })
                a["runs"] += 1
                a["noadopt_runs"] += 1 if not res["structural_calls"] else 0
                a["cnr"].update(res["cited_not_returned"])
                a["adopt_gap"].update(res["adoption_gap"])
                a["fallback"].update(res["fallback_reads"])
                a["degenerate"].update(f"{s}({sym})" for (s, sym, _n) in res["degenerate"])
                a["grep_runs"] += 1 if res["grep_calls"] else 0
                a["sense_calls"] += res["sense_call_count"]
                a["reads"] += res["n_read"]
                # nondeterminism tracking: symbol -> set of returned-count per run
                for (s, sym, n) in res["sense_calls"]:
                    if s in ("sense_blast", "sense_graph") and sym:
                        a["sense_symbols"][f"{s}:{sym}"].add(n)

    report(agg, gold_cache, n_runs, args)
    if args.json:
        dump_json(agg, gold_cache, args.json)


def _severity(repo_agg, gold):
    """Rank a cited-not-returned gold id: discriminator-group misses dominate."""
    score = 0
    for gid, cnt in repo_agg["cnr"].items():
        g = gold.get(gid, {})
        weight = 3 if g.get("group") not in ANCHOR_GROUPS else 1
        score += cnt * weight
    return score


def report(agg, gold_cache, n_runs, args):
    print(f"\n=== Sense-miss mine — stack={args.stack} model={args.model} "
          f"({n_runs} sense runs over {len(agg)} repos) ===\n")
    # rank repos by discriminator-weighted miss severity
    ranked = sorted(agg.items(),
                    key=lambda kv: _severity(kv[1], gold_cache.get(kv[0], {})),
                    reverse=True)
    for repo, a in ranked:
        gold = gold_cache.get(repo, {})
        runs = a["runs"]
        nondet = {k: sorted(v) for k, v in a["sense_symbols"].items() if len(v) > 1}
        sev = _severity(a, gold)
        adopt = ""
        if a["noadopt_runs"]:
            adopt = f", ⚠ NO-STRUCTURAL-CALL in {a['noadopt_runs']}/{runs} (adoption, not resolver)"
        print(f"## {repo}  ({runs} runs, resolver-sev={sev}, "
              f"avg {a['sense_calls']/runs:.1f} sense-calls + {a['reads']/runs:.0f} reads/run, "
              f"grep-fallback in {a['grep_runs']}/{runs}{adopt})")

        if a["adopt_gap"]:
            print(f"  [0] ADOPTION GAP — {a['noadopt_runs']}/{runs} runs made NO blast/graph call; "
                  f"{len(a['adopt_gap'])} gold ids cited via reads only. (Fix the SCENARIO/steering, not the resolver.)")
        if a["cnr"]:
            print("  [1] CITED-NOT-RETURNED (resolver misses — blast/graph WAS called but never returned this file):")
            for gid, cnt in sorted(a["cnr"].items(),
                                   key=lambda x: (gold.get(x[0], {}).get("group") not in ANCHOR_GROUPS, x[1]),
                                   reverse=True):
                g = gold.get(gid, {})
                disc = "★" if g.get("group") not in ANCHOR_GROUPS else " "
                rel = (g.get("relation", "") or "")[:72]
                print(f"      {disc} {gid:<26} {cnt}/{runs} runs  [{g.get('group','?')}]  {rel}")
        if nondet:
            print("  [3] NONDETERMINISTIC / DEGENERATE returns (same symbol, different return-counts across runs):")
            for k, counts in nondet.items():
                print(f"        {k}: returned {counts} across runs")
        if a["degenerate"]:
            empties = a["degenerate"].most_common()
            print(f"  [3] EMPTY returns: " +
                  ", ".join(f"{k}×{c}" for k, c in empties))
        # top fallback reads (coverage signal)
        top_fb = a["fallback"].most_common(6)
        if top_fb:
            tot = sum(a["fallback"].values())
            print(f"  [2] FALLBACK READS ({tot} total over {runs} runs; files no Sense call returned). Top:")
            for f, c in top_fb:
                print(f"        {c}×  {f}")
        print()

    print("Legend: ★ = discriminator-group miss (high product value). "
          "[1] resolver coverage · [2] under-coverage/trust · [3] resolution/determinism.\n"
          "These are DETECTION signals over already-paid transcripts — confirm each against the "
          "live index (resolve_oracle) before proposing a fix; ship only behind a differential bench.")


def dump_json(agg, gold_cache, path):
    out = {}
    for repo, a in agg.items():
        gold = gold_cache.get(repo, {})
        out[repo] = {
            "runs": a["runs"],
            "severity": _severity(a, gold),
            "cited_not_returned": [
                {"id": gid, "runs": cnt, "group": gold.get(gid, {}).get("group"),
                 "relation": gold.get(gid, {}).get("relation"),
                 "match": gold.get(gid, {}).get("match")}
                for gid, cnt in a["cnr"].most_common()
            ],
            "nondeterministic": {k: sorted(v) for k, v in a["sense_symbols"].items() if len(v) > 1},
            "empty_returns": dict(a["degenerate"]),
            "fallback_reads": dict(a["fallback"].most_common(20)),
            "grep_fallback_runs": a["grep_runs"],
        }
    with open(path, "w") as fh:
        json.dump(out, fh, indent=2)
    print(f"\nwrote {path}")


if __name__ == "__main__":
    main()
