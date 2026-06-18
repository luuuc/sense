#!/usr/bin/env python3
"""Seam-hunt helper v2 for the Rails-vertical win hunt.

The win pattern (calibrated on chatwoot/lobsters/discourse) is NOT binary
grep-invisibility. It is two things together:
  1. SCATTER   — the structural impact set spans many non-test dirs (no single
                 `ls` enumerates it).
  2. GREP-NOISE — the contract token is low-precision: grepping it repo-wide
                 returns far more files than the actual structural callers, so a
                 baseline cannot cleanly enumerate the set by text search.

A no-win is: few dirs (colocated), OR a clean high-precision token (grep nails
the set), OR no callers resolved at all (resolver gap).

Reports, per contract symbol:
  caller_files, non-test caller DIRS (scatter), grep-hit files repo-wide,
  PRECISION = caller_files / grep_hit_files (LOWER = more grep-hostile = better).

Usage:
  python3 bench/lib/seam_hunt.py <clone_dir> <Symbol> [token] [--hops N] [--conf F]
"""
import json, os, re, subprocess, sys, collections

def run(cmd, cwd):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)

def main():
    args = sys.argv[1:]
    clone = args[0]; symbol = args[1]
    rest = args[2:]
    token = None; hops = "3"; conf = "0.3"
    i = 0
    while i < len(rest):
        if rest[i] == "--hops": hops = rest[i+1]; i += 2
        elif rest[i] == "--conf": conf = rest[i+1]; i += 2
        else: token = rest[i]; i += 1
    if token is None:
        token = symbol.split("#")[-1].split(".")[-1].split("::")[-1]

    callers = {}  # file -> set(caller symbols)
    bl = run(["sense", "blast", symbol, "--min-confidence", conf, "--max-hops", hops, "--json"], clone)
    try:
        d = json.loads(bl.stdout)
        for key in ("direct_callers", "indirect_callers"):
            for c in d.get(key, []):
                f = c.get("file")
                if f: callers.setdefault(f, set()).add(c.get("symbol"))
    except Exception as e:
        print(f"[blast parse err] {e}", file=sys.stderr)
    gr = run(["sense", "graph", symbol, "--direction", "callers", "--depth", "2", "--json"], clone)
    try:
        d = json.loads(gr.stdout)
        for c in d.get("edges", {}).get("called_by", []):
            f = c.get("file")
            if f: callers.setdefault(f, set()).add(c.get("symbol"))
    except Exception as e:
        print(f"[graph parse err] {e}", file=sys.stderr)

    def is_test(f):
        return f.startswith("spec/") or "/spec/" in f or f.startswith("test/") or "/test/" in f
    def is_app(f):
        return not (is_test(f) or f.startswith("db/") or f.startswith("config/"))

    app_callers = {f: s for f, s in callers.items() if is_app(f)}
    dirs = collections.Counter(os.path.dirname(f) for f in app_callers)

    # repo-wide grep precision for the token (app code only, ruby files)
    gh = run(["grep", "-rlw", "--include=*.rb", token, "app", "lib"], clone)
    grep_files = [x for x in gh.stdout.splitlines() if x and not is_test(x)]
    grep_n = len(grep_files)
    prec = (len(app_callers) / grep_n) if grep_n else 0.0

    print(f"=== {symbol}  (token='{token}', hops={hops}, conf={conf}) ===")
    print(f"  app caller files : {len(app_callers)}   (total incl test/db: {len(callers)})")
    print(f"  SCATTER (dirs)   : {len(dirs)}   -> {dict(dirs.most_common(8))}")
    print(f"  GREP-NOISE       : '{token}' in {grep_n} app files   PRECISION={prec:.2f}  (lower=grep-hostile)")
    verdict = []
    if len(dirs) >= 5: verdict.append("SCATTERED")
    else: verdict.append(f"colocated({len(dirs)}dir)")
    if grep_n and prec <= 0.5: verdict.append("GREP-NOISY")
    elif grep_n: verdict.append("grep-clean")
    if not app_callers: verdict.append("RESOLVER-GAP?")
    print(f"  VERDICT          : {' + '.join(verdict)}")
    print(f"  --- app callers ---")
    for f in sorted(app_callers):
        print(f"    {f}  <- {', '.join(sorted(s for s in app_callers[f] if s))[:70]}")

if __name__ == "__main__":
    main()
