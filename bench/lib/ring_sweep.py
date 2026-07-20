#!/usr/bin/env python3
"""Multi-via retention-ring sweep: the Go win signature, one command per repo.

    ring_sweep.py <clone_dir> [--min-files 5] [--min-vias 5] [--top 30]

WHY THIS EXISTS (measured, 2026-07-20). Every Go cell in the campaign is
explained by ONE property of the anchor's laundered retention ring:

    WIN   dolt doltdb.DoltDB   18 import-proof files / 20 distinct vias  (paid +0.80)
          pebble Batch         11 files / multi-via                      (paid +0.67)
    DEAD  grpc-go, teleport, temporal, navidrome, nats: ring saturates on ONE via,
          or the rows are test-file satisfiers.

The reason is THE GO-SATISFACTION LAW: Go has no `implements`, so a baseline
finds implementors by grepping a METHOD name. A single-interface ring is
therefore one grep away (traefik Traceable: 31 deps, 30 "grep-invisible" on the
interface token, killed by `grep -rl GetTracingInformation` = 32 files). Only a
ring spread across MANY distinct narrow interfaces has no single enumerating
grep; dolt's paid gold spans 5+ interfaces and the best single method grep
covers 3 of its 12 files.

So the screen is: rank anchors by (prod ring rows, DISTINCT vias), then hand the
survivors to admission_gate.py. Anchors that fail here need no session and no
gate run. Test-file satisfiers are excluded (the temporal law: one test struct
embedding a dozen interfaces fabricated 52 of 100 rows).

Output is a ranked table; exit 0 always (a screen reports, it does not gate).
"""

import argparse
import collections
import json
import os
import sqlite3
import subprocess
import sys

LIB = os.path.dirname(os.path.abspath(__file__))


def candidate_anchors(clone, top):
    """Types/interfaces/classes with the most distinct prod dependent files."""
    db = os.path.join(clone, ".sense", "index.db")
    if not os.path.exists(db):
        sys.exit(f"no index at {db}; scan the clone first")
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    rows = con.execute("""
        select t.name, t.kind, f2.path, count(distinct f.path) dep_files
        from sense_edges e
        join sense_symbols t on t.id = e.target_id
        join sense_symbols s on s.id = e.source_id
        join sense_files f on f.id = s.file_id
        join sense_files f2 on f2.id = t.file_id
        where t.kind in ('interface', 'class', 'type')
          and f.path not like '%_test.go'
          and f2.path not like '%_test.go'
        group by t.id
        order by dep_files desc, t.name, f2.path
        limit ?""", (top,)).fetchall()
    con.close()
    return rows


def qualify(clone, name, path):
    """Sense resolves Go anchors by package-qualified name; a bare name misses.

    Validation catch (2026-07-20): the first cut of this sweep passed bare names
    and reported an EMPTY ring for dolt's DoltDB, the anchor of a paid win with
    a 53-row ring. Always qualify with the defining file's `package` clause.
    """
    try:
        with open(os.path.join(clone, path), encoding="utf-8",
                  errors="replace") as fh:
            for line in fh:
                if line.startswith("package "):
                    return f"{line.split()[1].strip()}.{name}"
    except OSError:
        pass
    return name


def blast_many(clone, symbols):
    """One MCP stdio session for all anchors; the CLI diverges by design.

    Each call carries the anchor's DEFINING file as the `file` disambiguator:
    consul has several `Service` / `Filter` symbols, and without the hint the
    blast returns an ambiguity payload (or, worse, a different symbol run to
    run). Paired with the deterministic ORDER BY above, two runs of this sweep
    on an unchanged index now produce byte-identical tables; the first cut did
    not, and reported `namedFilter` as both 0 rows and 29 rows in two runs
    minutes apart.
    """
    calls = [{"name": "sense_blast",
              "arguments": {"symbol": sym, "min_confidence": 0.7, "file": hint}}
             for sym, hint in symbols]
    out = subprocess.run([sys.executable, os.path.join(LIB, "mcp_probe.py"),
                          clone, json.dumps(calls)],
                         capture_output=True, text=True).stdout
    # Key by CALL ID, never by arrival order. The stdio server may answer out
    # of order: the first cut appended in arrival order and silently attributed
    # one anchor's ring to its neighbour; two runs minutes apart reported
    # consul `namedFilter` as 29 rows and as 2 rows, with the 29 sliding onto
    # whichever anchor happened to sit at that offset.
    by_id = {}
    for chunk in out.split("═══ call id=")[1:]:
        head, _, rest = chunk.partition("═══")
        try:
            cid = int(head.strip())
        except ValueError:
            continue
        try:
            by_id[cid] = json.loads(rest.strip())
        except json.JSONDecodeError:
            by_id[cid] = {}
    return [by_id.get(i, {}) for i in range(1, len(calls) + 1)]


def ring_profile(data):
    rows = data.get("retained_via_interfaces") or []
    prod = [r for r in rows
            if "_test.go" not in (r.get("ref") or "")
            and "_test.go" not in (r.get("carrier") or "")]
    vias = collections.Counter(r.get("via") for r in prod)
    carriers = collections.Counter(r.get("carrier") for r in prod)
    files = {(r.get("ref") or "").rsplit(":", 1)[0] for r in prod}
    top_share = (vias.most_common(1)[0][1] / len(prod)) if prod else 0.0
    # TOP-CARRIER SHARE: the fabrication tell (the vault class, below).
    # Via-diversity alone is NOT purity: vault framework.Backend showed 65 files
    # across 21 vias (the biggest ring in the pool) and was FABRICATED: 31 of 62
    # rows rode one carrier, `vault.ForwardedWriter`, which holds a
    # `logical.Storage` and has no relation to framework.Backend at all
    # (audit/backend.go, a credited holder, contains zero `framework.` tokens).
    # One concrete satisfying many DIFFERENT narrow interfaces is the same
    # generic-interface chaining that fabricated temporal's ring. consul.Server,
    # by contrast, spreads across 10 carriers that were each read in source and
    # each hold *Server.
    # Only rows that actually declare a carrier count: the field is optional in
    # the payload (dolt's rows are {ref, symbol, via} with NO carrier), and the
    # first cut divided by len(prod), so "field absent" read as "one carrier at
    # 100%" and FLAGGED THE PAID WIN. Calibration caught it; the rule below is a
    # flag, never a rejection, because concentration alone is not fabrication,
    # only reading the carrier in source settles it.
    withc = [r for r in prod if r.get("carrier")]
    top_carrier = (collections.Counter(r["carrier"] for r in withc)
                   .most_common(1)[0][1] / len(withc)) if withc else None
    return {"rows": len(prod), "files": len(files), "vias": len(vias),
            "top_via_share": round(top_share, 2),
            "top_carrier_share": (round(top_carrier, 2)
                                  if top_carrier is not None else None),
            "test_rows_cut": len(rows) - len(prod)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("clone")
    ap.add_argument("--min-files", type=int, default=5)
    ap.add_argument("--min-vias", type=int, default=5)
    ap.add_argument("--top", type=int, default=30)
    a = ap.parse_args()

    cands = candidate_anchors(a.clone, a.top)
    names = [qualify(a.clone, c[0], c[2]) for c in cands]
    probes = list(zip(names, [c[2] for c in cands]))
    print(f"# ring sweep: {a.clone}  ({len(names)} anchors, "
          f"bar: ≥{a.min_files} prod files across ≥{a.min_vias} distinct vias)\n")
    data = blast_many(a.clone, probes)
    survivors = []
    print(f"{'anchor':32s} {'kind':10s} {'rows':>5s} {'files':>6s} {'vias':>5s} "
          f"{'top-via':>8s} {'top-carr':>9s} {'test-cut':>9s}  verdict")
    for (name, kind, path, _dep), qname, d in zip(cands, names, data):
        if not d or not d.get("symbol"):
            print(f"{name:32s} {kind:10s} {'-':>5s} {'-':>6s} {'-':>5s} "
                  f"{'-':>8s} {'-':>9s}  unresolved/ambiguous")
            continue
        p = ring_profile(d)
        ok = p["files"] >= a.min_files and p["vias"] >= a.min_vias
        # saturation: one via carrying most of the ring is the gms/navidrome class
        if ok and p["top_via_share"] > 0.6:
            ok, why = False, f"saturated ({p['top_via_share']} on one via)"
        elif ok and (p["top_carrier_share"] or 0) > 0.4:
            why = (f"CANDIDATE ⚠ {p['top_carrier_share']} of the ring on ONE carrier "
                   "read that carrier in source before curating gold (vault class)")
        elif ok:
            why = "CANDIDATE → run admission_gate.py"
        else:
            why = "under bar"
        if ok:
            survivors.append((qname, path, p))
        print(f"{name:32s} {kind:10s} {p['rows']:5d} {p['files']:6d} {p['vias']:5d} "
              f"{p['top_via_share']:8.2f} "
              f"{('%.2f' % p['top_carrier_share']) if p['top_carrier_share'] is not None else '-':>9s} "
              f"{p['test_rows_cut']:9d}  {why}")

    print(f"\n{len(survivors)} candidate(s) above the bar.")
    for name, path, p in survivors:
        print(f"  python3 bench/lib/admission_gate.py {a.clone} '{name}' --file {path}")


if __name__ == "__main__":
    main()
