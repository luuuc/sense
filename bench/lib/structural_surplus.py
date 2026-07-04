#!/usr/bin/env python3
"""Structural-surplus preflight — the Sense-FIRST scenario-hunt instrument ($0, no LLM).

Scenario-crafting is NOT "find grep-hell by grepping around" (circular: grep-shaped
nomination only surfaces seams grep can also unwind). It is "measure STRUCTURAL SURPLUS":

    surplus(contract) = { deps the agent's BUDGETED sense_blast returns in ONE call }
                        - { deps a GENEROUS baseline grep+read can reconstruct }
                        filtered to deps the agent will actually CITE (production).

HIGH surplus manifests as grep-hell: dependents close to IMPOSSIBLE to find with grep
(reached via an edge grep cannot follow even iteratively, or behind a token too noisy
to trace). That IS the win. Surplus ~= 0 repo-wide => a boundary repo: ship the honest
reach-at-parity row (manifesto §13) or swap the same-type backup (§8); do NOT grind or
bench a predicted tie.

THE TWO HALVES
  Sense set S  — the DEPENDENT set from the AGENT-FACING, budget-trimmed `sense_blast`
    (driven over `sense mcp` stdio, NOT the raw CLI/index — the MCP token budget trims
    the blast differently, and the agent only ever sees the trimmed set).
  Grep set G   — the GENEROUS baseline falsifier: from the contract name, iterate ~N hops
    of caller-tracing (grep the name -> map each hit line to its enclosing symbol via the
    index -> grep THAT name, ...), union-grepping sibling names and reading small files
    whole. Generous on CLEAN names (a thorough Opus reconstructs those). But a frontier
    name whose grep DROWNS (> --noise-cap hits) reconstructs nothing further — modelling
    the noisy-token lever (grep gives no clean caller list to trace, so hand-tracing
    dead-ends). If even this generous-but-realistic grep cannot reach a dep, Opus can't
    cheaply either -> it is real surplus.

MODES
  --symbol NAME [--file F]   surplus for one contract (the hunt primitive)
  --scenario                 use the scenario's contract_symbol + score its DISCRIMINATOR
                             gold: how many gold deps are retrievable (in S) AND grep-hard
                             (not in G) -> a pre-bench prediction of the `dependents` delta
  --nominate K               Sense-nominate: rank K structurally-richest contracts by
                             transitive caller fan-in+scatter, then score each (the hunt)
  --calibrate                scenario-score every scenario in the vertical (retrodict the
                             benched outcomes to validate the falsifier)

Calibrate the falsifier by retrodicting benched outcomes (saleor +0.62, rails +0.56,
sentry +0.00, lobsters tie): if the metric reproduces them it is trustworthy; where it
disagrees, that gap teaches what the falsifier misses. DETECTOR, never the ship gate
(the differential bench stays that). See memory feedback_structural_surplus_grep_hell.

Usage:
  python3 bench/lib/structural_surplus.py sentry --symbol sync_status_outbound \
      --file src/sentry/integrations/tasks/sync_status_outbound.py
  python3 bench/lib/structural_surplus.py saleor --scenario
  python3 bench/lib/structural_surplus.py sentry --nominate 12
  python3 bench/lib/structural_surplus.py --calibrate --stack python-django
"""
import argparse
import json
import os
import subprocess
import sys
import sqlite3

sys.path.insert(0, os.path.dirname(__file__))
import yaml  # noqa: E402

SENSE_REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
CLONES = os.environ.get(
    "SENSE_CLONES", os.path.join(os.path.dirname(SENSE_REPO), "sense-benchmark", "sense")
)
VERTICALS = os.path.join(SENSE_REPO, "bench", "verticals")
SENSE_BIN = os.environ.get("SENSE_BIN", os.path.expanduser("~/.local/bin/sense"))

ANCHOR_GROUPS = {"contract", "context", "surface", "teardown"}
DEFAULT_HOPS = 4          # generous: baseline traces one hop deeper than blast's max-hops 3
DEFAULT_NOISE_CAP = 200   # grep(name) > this many hits => drowns, no clean caller list
DEFAULT_SMALL = 150       # a file under this many lines is "read whole" by the baseline
SURPLUS_WIN, SURPLUS_TIE = 0.50, 0.20  # fraction of citable Sense deps that are grep-hard


# ----------------------------------------------------------------------------- index
class Index:
    """The repo's .sense index: file<->symbol geometry for the enclosing-symbol map."""

    def __init__(self, clone):
        db = sqlite3.connect(os.path.join(clone, ".sense", "index.db"))
        self.by_file = {}   # path -> [(line_start, line_end, name, id)]
        self.name_ids = {}  # name -> set(id)
        rows = db.execute(
            "SELECT s.id, s.name, f.path, s.line_start, s.line_end "
            "FROM sense_symbols s JOIN sense_files f ON f.id = s.file_id"
        ).fetchall()
        for sid, name, path, lo, hi in rows:
            self.by_file.setdefault(path, []).append((lo or 0, hi or 0, name, sid))
            self.name_ids.setdefault(name, set()).add(sid)
        for path in self.by_file:
            self.by_file[path].sort()
        db.close()

    def enclosing(self, path, line):
        """Name of the innermost symbol whose [line_start, line_end] contains `line` —
        what a baseline sees when it opens the file at a grep hit and reads which
        function it landed in. None if the hit is module-level / outside any symbol."""
        best = None
        for lo, hi, name, _sid in self.by_file.get(path, []):
            if lo <= line <= hi and (best is None or lo > best[0]):
                best = (lo, name)
        return best[1] if best else None


# ------------------------------------------------------------------------- sense side
def sense_blast_budgeted(clone, symbol, file_arg=None):
    """The AGENT-FACING blast: drive `sense mcp` over stdio and return the token-budgeted
    dependent set (direct + indirect callers), as the agent actually receives it."""
    args = {"symbol": symbol}
    if file_arg:
        args["file"] = file_arg
    reqs = [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize",
         "params": {"protocolVersion": "2024-11-05", "capabilities": {},
                    "clientInfo": {"name": "surplus", "version": "0"}}},
        {"jsonrpc": "2.0", "method": "notifications/initialized"},
        {"jsonrpc": "2.0", "id": 2, "method": "tools/call",
         "params": {"name": "sense_blast", "arguments": args}},
    ]
    inp = "\n".join(json.dumps(r) for r in reqs) + "\n"
    try:
        p = subprocess.run([SENSE_BIN, "mcp"], input=inp, cwd=clone,
                           capture_output=True, text=True, timeout=120)
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return []
    for line in p.stdout.splitlines():
        try:
            m = json.loads(line)
        except json.JSONDecodeError:
            continue
        if m.get("id") == 2 and "result" in m:
            try:
                d = json.loads(m["result"]["content"][0]["text"])
            except (KeyError, IndexError, json.JSONDecodeError):
                return []
            return d.get("direct_callers", []) + d.get("indirect_callers", [])
    return []


def is_prod(path):
    """Citable production python (source-root agnostic: sentry=src/sentry, saleor=saleor,
    django=django, ...). Drops tests, migrations, and non-python."""
    if not path.endswith(".py"):
        return False
    base = path.rsplit("/", 1)[-1]
    return not (
        "/tests/" in path or path.startswith("tests/") or "/test/" in path
        or base.startswith("test_") or base.endswith("_test.py")
        or "/migrations/" in path
    )


def prod_dep_files(callers):
    """Citable filter: production source files only (drop tests / the ref-less)."""
    return {c["ref"].split(":")[0] for c in callers
            if c.get("ref") and is_prod(c["ref"].split(":")[0])}


# -------------------------------------------------------------------------- grep side
def git_grep(clone, name):
    """Whole-word grep for `name` across python source — the baseline's one move."""
    try:
        p = subprocess.run(
            ["git", "grep", "-nw", "--", name, "--", "*.py"],
            cwd=clone, capture_output=True, text=True, timeout=60)
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return []
    hits = []
    for line in p.stdout.splitlines():
        parts = line.split(":", 2)
        if len(parts) >= 2 and parts[1].isdigit():
            hits.append((parts[0], int(parts[1])))
    return hits


def file_line_count(clone, path):
    try:
        with open(os.path.join(clone, path), "r", errors="ignore") as fh:
            return sum(1 for _ in fh)
    except OSError:
        return 10 ** 9


def grep_reconstructable(clone, idx, seed_name, hops, noise_cap, small,
                         targets=None, max_greps=1500):
    """The GENEROUS falsifier. BFS caller-tracing from the contract name: grep a name ->
    map hits to enclosing symbols (baseline opens the file, sees the function) -> those
    become the next frontier. Read small files whole. A name whose grep DROWNS
    (> noise_cap hits) reconstructs nothing further (the noisy-token lever). NOT frontier-
    capped: within a few hops Opus traces every DISTINCTIVE branch (the sentry sync_status
    bench proved a real baseline reaches all 5 branches through the update_groups bridge);
    only NOISY names drown, and that is what noise_cap models.

    Perf: the falsifier's only job is to decide which `targets` files grep can reach, so it
    EARLY-STOPS the moment every target is covered (a god-object hub's deps are found at hop
    0-1; the old full-tree expansion past them was the stall). `max_greps` is a generous
    safety backstop; if it trips before all targets are reached the uncovered ones are
    reported grep-hard and `truncated=True` (surfaced, never silent). Returns
    (reached_files, truncated)."""
    reached_files, seen_names = set(), set()
    frontier = {seed_name}
    greps, truncated = 0, False
    for _ in range(hops + 1):          # +1: hop 0 greps the contract name itself
        nxt = set()
        for name in frontier:
            if name in seen_names or not name:
                continue
            seen_names.add(name)
            greps += 1
            if greps > max_greps:
                truncated = True
                break
            hits = git_grep(clone, name)
            if len(hits) > noise_cap:  # drowns: no clean caller list to trace through
                continue
            for path, line in hits:
                if not is_prod(path):
                    continue
                enc = idx.enclosing(path, line)
                if enc:
                    reached_files.add(path)
                    nxt.add(enc)
                if file_line_count(clone, path) < small:   # baseline reads it whole
                    for _l0, _l1, sname, _sid in idx.by_file.get(path, []):
                        reached_files.add(path)
                        nxt.add(sname)
            if targets and targets <= reached_files:   # every target covered — done
                return reached_files, truncated
        if truncated:
            break
        frontier = nxt - seen_names
    return reached_files, truncated


# ------------------------------------------------------------------------- the metric
def compute_surplus(clone, idx, symbol, file_arg, hops, noise_cap, small):
    """S (budgeted sense_blast, prod) minus G (generous grep) = citable structural surplus."""
    callers = sense_blast_budgeted(clone, symbol, file_arg)
    S = prod_dep_files(callers)
    contract_file = file_arg or None
    S.discard(contract_file)
    G, truncated = grep_reconstructable(clone, idx, symbol, hops, noise_cap, small, targets=S)
    surplus = sorted(f for f in S if f not in G)
    reconstructable = sorted(f for f in S if f in G)
    frac = (len(surplus) / len(S)) if S else 0.0
    return {
        "symbol": symbol, "file": file_arg,
        "sense_dep_files": len(S), "grep_reconstructable": len(reconstructable),
        "surplus_files": surplus, "surplus_count": len(surplus), "surplus_frac": round(frac, 3),
        "grep_truncated": truncated, "verdict": _verdict(len(S), frac),
    }


def _verdict(n_sense, frac):
    if n_sense < 3:
        return "THIN (too few citable Sense deps to separate the arms)"
    if frac >= SURPLUS_WIN:
        return "WIN-SHAPED (grep-hell: most Sense deps are grep-hard)"
    if frac >= SURPLUS_TIE:
        return "MARGINAL (some grep-hard deps; fragile, curate hard)"
    return "TIE-SHAPED (grep reconstructs the Sense set — boundary repo/seam)"


# ---------------------------------------------------------------------- scenario mode
def load_scenario(stack, repo):
    f = os.path.join(VERTICALS, stack, "scenarios", f"{repo}.yaml")
    return yaml.safe_load(open(f)) if os.path.exists(f) else None


def discriminator_files(doc):
    """The gold's discriminator group (largest non-anchor group) -> its match paths."""
    by_group = {}
    for t in doc.get("gold", []):
        m = t.get("match")
        if m:
            by_group.setdefault(t.get("group", "?"), []).extend(m)
    disc = [g for g in by_group if g not in ANCHOR_GROUPS]
    if not disc:
        return set(), "?"
    group = max(disc, key=lambda g: len(by_group[g]))
    return set(by_group[group]), group


def score_scenario(clone, idx, doc, hops, noise_cap, small):
    """Predict the `dependents` delta: of the scenario's discriminator gold, how many are
    RETRIEVABLE (in the budgeted Sense set) AND grep-hard (not grep-reconstructable)."""
    symbol = doc.get("contract_symbol")
    file_arg = doc.get("contract_file")
    gold, group = discriminator_files(doc)
    callers = sense_blast_budgeted(clone, symbol, file_arg)
    S = prod_dep_files(callers)
    G, truncated = grep_reconstructable(clone, idx, symbol, hops, noise_cap, small, targets=set(gold))
    rows = []
    for g in sorted(gold):
        retr = g in S
        hard = g not in G
        rows.append({"file": g, "retrievable": retr, "grep_hard": hard,
                     "discriminates": retr and hard})
    disc = [r for r in rows if r["discriminates"]]
    retr = [r for r in rows if r["retrievable"]]
    predicted = (len(disc) / len(gold)) if gold else 0.0
    return {
        "symbol": symbol, "group": group, "gold": len(gold),
        "retrievable": len(retr), "grep_hard": sum(1 for r in rows if r["grep_hard"]),
        "discriminating": len(disc), "predicted_dependents_recall_gap": round(predicted, 3),
        "grep_truncated": truncated, "verdict": _verdict(len(retr), predicted), "rows": rows,
    }


# ---------------------------------------------------------------------- nominate mode
def nominate(clone, hops, ceil):
    """PHASE 1 (cheap, Sense-only, NO grep): rank candidate contracts by the STRUCTURE
    only Sense sees, across ALL the dependency edge kinds that produce grep-hell surplus,
    each a distinct lens:
      * calls    -> CALL-HUB   (services / helpers everything invokes)
      * composes -> MODEL      (Django model hubs reached via FK/O2O/M2M reverse deps)
      * inherits -> POLY-BASE  (base classes with scattered subclasses)
    Ranking on calls alone hid the model/poly wins (saleor's ProductVariant is a `class`
    reached via `composes`, not calls). Each candidate carries its per-lens direct
    in-degree + transitive fan-in x scatter + a tag, so a human can ANALYZE the richer
    menu and pick which to grep-falsify (phase 2). Filtered to the citable band
    [MIN_FANIN, ceil]: below is thin, ABOVE is an infra god-object (low citability, slow
    to grep)."""
    db = sqlite3.connect(os.path.join(clone, ".sense", "index.db"))
    fp = {r[0]: r[1] for r in db.execute("SELECT s.id, f.path FROM sense_symbols s "
                                         "JOIN sense_files f ON f.id=s.file_id").fetchall()}
    prod = {sid for sid, path in fp.items() if is_prod(path)}
    meta = {r[0]: (r[1], r[2]) for r in db.execute(
        "SELECT s.id, s.name, s.kind FROM sense_symbols s").fetchall() if r[0] in prod}
    rows = db.execute(
        "SELECT source_id, target_id, kind, confidence FROM sense_edges "
        "WHERE kind IN ('calls','composes','inherits')").fetchall()
    db.close()
    rev_c, rev_m, rev_i, rev_all = {}, {}, {}, {}   # calls / composes(model) / inherits / merged
    for s, t, kind, conf in rows:
        if s not in prod or t not in prod:
            continue
        if kind == "calls" and conf not in (1.0, 0.9, 0.8):
            continue
        {"calls": rev_c, "composes": rev_m, "inherits": rev_i}[kind].setdefault(t, []).append(s)
        rev_all.setdefault(t, set()).add(s)

    RANK_HOPS = min(hops, 3)   # coarse structural ranker; the full surplus uses `hops`
    RANK_CAP = 600             # bound the BFS so a hub in a dense graph can't stall it
    def fanin(root):
        seen, frontier = {root}, [root]
        for _ in range(RANK_HOPS):
            nxt = []
            for t in frontier:
                for s in rev_all.get(t, ()):
                    if s not in seen:
                        seen.add(s)
                        nxt.append(s)
                if len(seen) >= RANK_CAP:
                    break
            frontier = nxt
            if len(seen) >= RANK_CAP:
                break
        deps = [s for s in seen if s != root]
        subs = {"/".join(fp.get(s, "").split("/")[:4]) for s in deps}
        return len(deps), len(subs)

    MIN_DIRECT, MIN_FANIN, MIN_SCATTER = 5, 10, 4
    scored = []
    for sid in rev_all:
        if sid not in meta or meta[sid][1] not in ("function", "method", "class"):
            continue
        cin, min_, iin = len(rev_c.get(sid, ())), len(rev_m.get(sid, ())), len(rev_i.get(sid, ()))
        if cin + min_ + iin < MIN_DIRECT:
            continue
        n, subs = fanin(sid)
        if MIN_FANIN <= n <= ceil and subs >= MIN_SCATTER:
            tag = max((("CALL", cin), ("MODEL", min_), ("POLY", iin)), key=lambda x: x[1])[0]
            scored.append({"score": n * subs, "fanin": n, "scatter": subs,
                           "call_in": cin, "model_in": min_, "inherit_in": iin,
                           "tag": tag, "symbol": meta[sid][0], "file": fp.get(sid, "")})
    scored.sort(key=lambda r: r["score"], reverse=True)
    return scored


# --------------------------------------------------------------------------- reporting
def report_symbol(res):
    print(f"\n=== structural surplus: {res['symbol']}"
          f"{(' @ ' + res['file']) if res['file'] else ''} ===")
    print(f"  budgeted sense_blast citable deps : {res['sense_dep_files']}")
    print(f"  grep-reconstructable (generous)   : {res['grep_reconstructable']}")
    print(f"  SURPLUS (grep-hard) files         : {res['surplus_count']} "
          f"({res['surplus_frac']*100:.0f}% of Sense deps)")
    if res.get("grep_truncated"):
        print("  NOTE: grep budget hit — some 'surplus' files may be reachable with more "
              "tracing (treat as an upper bound)")
    print(f"  VERDICT: {res['verdict']}")
    for f in res["surplus_files"]:
        print(f"    + {f}")


def report_scenario(repo, res):
    print(f"\n=== scenario prediction: {repo} (contract {res['symbol']}, "
          f"'{res['group']}' group) ===")
    print(f"  discriminator gold        : {res['gold']}")
    print(f"  retrievable (in Sense set): {res['retrievable']}")
    print(f"  grep-hard (not in G)      : {res['grep_hard']}")
    print(f"  DISCRIMINATING (both)     : {res['discriminating']}/{res['gold']}  "
          f"=> predicted dependents gap ~{res['predicted_dependents_recall_gap']:+.2f}")
    if res.get("grep_truncated"):
        print("  NOTE: grep budget hit — 'grep-hard' count may be an upper bound")
    print(f"  VERDICT: {res['verdict']}")
    for r in res["rows"]:
        tag = "WIN " if r["discriminates"] else ("gettable" if r["retrievable"] else "MISS(Sense)")
        print(f"    [{tag:11s}] retr={int(r['retrievable'])} hard={int(r['grep_hard'])}  {r['file']}")


def main():
    ap = argparse.ArgumentParser(description="Structural-surplus preflight (Sense-first hunt).")
    ap.add_argument("repo", nargs="?", help="repo slug (clone under $SENSE_CLONES)")
    ap.add_argument("--stack", default="python-django")
    ap.add_argument("--symbol")
    ap.add_argument("--file", dest="file_arg")
    ap.add_argument("--scenario", action="store_true")
    ap.add_argument("--nominate", type=int, default=0,
                    help="grep-falsify the top-N Sense-ranked candidates (phase 2)")
    ap.add_argument("--rank-only", action="store_true",
                    help="phase 1 ONLY: print the Sense ranking, no grep (you analyze + pick)")
    ap.add_argument("--fanin-ceil", type=int, default=250,
                    help="drop candidates with fan-in above this (infra god-objects)")
    ap.add_argument("--calibrate", action="store_true")
    ap.add_argument("--hops", type=int, default=DEFAULT_HOPS)
    ap.add_argument("--noise-cap", type=int, default=DEFAULT_NOISE_CAP)
    ap.add_argument("--small", type=int, default=DEFAULT_SMALL)
    ap.add_argument("--json", default="")
    args = ap.parse_args()

    if not os.path.exists(SENSE_BIN):
        sys.exit(f"sense binary not found at {SENSE_BIN} (set SENSE_BIN=)")

    out = {}
    if args.calibrate:
        import glob
        for f in sorted(glob.glob(os.path.join(VERTICALS, args.stack, "scenarios", "*.yaml"))):
            if f.endswith(".rubric.yaml") or ".bak" in f:
                continue
            repo = os.path.basename(f)[:-5]
            clone = os.path.join(CLONES, repo)
            if not os.path.exists(os.path.join(clone, ".sense", "index.db")):
                continue
            doc = yaml.safe_load(open(f))
            if not doc.get("contract_symbol"):
                continue
            res = score_scenario(clone, Index(clone), doc, args.hops, args.noise_cap, args.small)
            out[repo] = res
            report_scenario(repo, res)
    else:
        if not args.repo:
            ap.error("repo is required unless --calibrate")
        clone = os.path.join(CLONES, args.repo)
        if not os.path.exists(os.path.join(clone, ".sense", "index.db")):
            sys.exit(f"no .sense index at {clone}")
        idx = Index(clone)
        if args.nominate or args.rank_only:
            # PHASE 1 (cheap, Sense-only, multi-lens): rank the citable band, print instantly.
            ranked = nominate(clone, args.hops, args.fanin_ceil)
            print(f"\n=== PHASE 1: Sense-ranked candidates for {args.repo}  "
                  f"(fan-in x scatter across calls/composes/inherits, fan-in<= {args.fanin_ceil}) ===")
            print(f"  {'tag':5s} {'fanin':>5s} {'scat':>4s} "
                  f"{'call':>4s} {'mdl':>4s} {'inh':>4s}  symbol / file")
            shown = ranked if args.rank_only else ranked[:max(args.nominate * 3, args.nominate)]
            for r in shown:
                print(f"  {r['tag']:5s} {r['fanin']:5d} {r['scatter']:4d} "
                      f"{r['call_in']:4d} {r['model_in']:4d} {r['inherit_in']:4d}  "
                      f"{r['symbol']}  {r['file']}")
            out = {"phase1_ranked": ranked}
            if args.rank_only:
                print("\n(--rank-only: no grep. Analyze the lenses, pick promising rows, then run "
                      "`--symbol <name> --file <path>` to grep-falsify each.)")
            else:
                # PHASE 2 (expensive, grep-falsify): only the top-N shortlist.
                print(f"\n=== PHASE 2: grep-falsify the top {args.nominate} ===")
                phase2 = []
                for r in ranked[:args.nominate]:
                    res = compute_surplus(clone, idx, r["symbol"], r["file"],
                                          args.hops, args.noise_cap, args.small)
                    print(f"  {r['tag']:5s} surplus={res['surplus_count']:2d}"
                          f"/{res['sense_dep_files']:2d} ({res['surplus_frac']*100:3.0f}%)  "
                          f"{res['verdict'].split(' ')[0]:12s}  {r['symbol']}  {r['file']}")
                    phase2.append(res)
                out["phase2_surplus"] = phase2
        elif args.scenario:
            doc = load_scenario(args.stack, args.repo)
            if not doc:
                sys.exit(f"no scenario for {args.repo}")
            res = score_scenario(clone, idx, doc, args.hops, args.noise_cap, args.small)
            out = res
            report_scenario(args.repo, res)
        elif args.symbol:
            res = compute_surplus(clone, idx, args.symbol, args.file_arg,
                                  args.hops, args.noise_cap, args.small)
            out = res
            report_symbol(res)
        else:
            ap.error("one of --symbol / --scenario / --nominate / --calibrate is required")

    if args.json:
        json.dump(out, open(args.json, "w"), indent=2)
        print(f"\nwrote {args.json}")


if __name__ == "__main__":
    main()
