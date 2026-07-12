#!/usr/bin/env python3
"""Loop 2 admission gate — per-candidate measurement, before a repo joins the slate.

    admission_gate.py <clone_dir> <Symbol> [--file HINT] [--sense BIN] [--json OUT]

Runs the mechanical half of the seven-bar gate
(.doc/launch/00-next-vertical/loops/02-repo-admission.md) against a candidate's
built index and emits a verdict block for repos.md. Bars 1-4 are measured here;
bars 5-7 (memorization screen, slot fit, channel/banner) print as checklist
lines — they are judgment, and the gate never fakes judgment as measurement.

Measurements (laws from bench/results/loss-anatomy.md baked in):
  bar 2 — seam existence, TWO components (the sentry calibration lesson —
          naive invisibility alone would reject the banked +0.60 win):
          (a) grep-invisible dependents: prod dependents (blast @0.3) whose
              file text lacks the contract token literally;
          (b) grep-noise / PRECISION: dependents ÷ repo-wide prod files
              containing the token (seam_hunt v3's win-pattern: LOWER
              precision = the baseline cannot cleanly enumerate the set).
          Threshold: calibrated by backtest, not guessed; raw numbers printed.
  bar 3 — covering-pattern battery: does ONE declared textual pattern
          transcribe the dependent set? Battery per anatomy law #6: the token
          itself, subclass headers, typed-field annotations (`x: Token`,
          the sentry law), harvested Django accessors (related_name +
          lowercase-plural, the pretix law), file-layout convention
          (basename/dir, the healthchecks law). best_cover >= 0.8 = covered.
          Depth is NOT hostility (anatomy law #4) — nothing here scores hops.
  bar 4 — blast-retrievable at 0.3 AND 0.7 inside the per-tool budget
          (~32KB ≈ 8k tokens); a broken anchor (saleor) fails loudly here.
          PLUS a graph-side fold probe (the #191 lesson, 2026-07-12): blast
          dep sets were BYTE-IDENTICAL across the fold collapse, only
          `graph --direction callers` saw it (called_by 126 → 1). Collapse
          signature = called_by tiny while blast direct callers are numerous;
          a healthy small seam has BOTH small. FAIL when called_by <=
          GRAPH_FOLD_FLOOR and direct >= GRAPH_FOLD_RATIO × max(called_by, 1).

The gate is slot-aware downstream: win-pillar slots are gated on 1-5, small
slots are measured-not-rejected (§7.0 ballast). That policy lives in the
one-pager; this script only measures and never lowers a bar to fill a slot.
"""

import argparse
import json
import os
import re
import subprocess
import sys

BUDGET_BYTES = 32_000          # ≈ the 8k-token per-tool response budget
COVER_THRESHOLD = 0.8          # one pattern transcribing ≥80% of deps = covered
PRECISION_FLOOR = 0.3          # ...but only if it enumerates mostly-deps (usable)
GRAPH_FOLD_FLOOR = 5           # called_by at/below this is suspicious... (collapse=1, healthy min=25)
GRAPH_FOLD_RATIO = 10          # ...when blast direct >= 10× it (collapse=60×, healthy max=1.08×)
TEST_PATH = re.compile(r"(^|/)(tests?|spec|specs|testing)(/|_)|_test\.|\btest_")


def run_blast(clone, sense, symbol, conf, file_hint):
    cmd = [sense, "blast", symbol, "--json", "--min-confidence", str(conf)]
    if file_hint:
        cmd += ["--file", file_hint]
    p = subprocess.run(cmd, cwd=clone, capture_output=True, text=True,
                       stdin=subprocess.DEVNULL)
    raw = p.stdout
    try:
        data = json.loads(raw)
    except ValueError:
        data = None
    return {"conf": conf, "ok": p.returncode == 0 and data is not None,
            "bytes": len(raw), "data": data,
            "err": (p.stderr or raw)[:300] if data is None else ""}


def run_graph_callers(clone, sense, symbol, file_hint):
    """Default-floor `graph --direction callers` for the anchor. Blast alone is
    blind to a graph-side fold collapse (#191: blast byte-identical, graph
    called_by 126 → 1); the oracle's blast∪graph union caught it — this is
    that union's cheap half."""
    cmd = [sense, "graph", symbol, "--direction", "callers", "--json"]
    if file_hint:
        cmd += ["--file", file_hint]
    p = subprocess.run(cmd, cwd=clone, capture_output=True, text=True,
                       stdin=subprocess.DEVNULL)
    try:
        data = json.loads(p.stdout)
    except ValueError:
        data = None
    ok = p.returncode == 0 and data is not None
    called_by = len((data.get("edges") or {}).get("called_by") or []) if ok else 0
    return {"ok": ok, "called_by": called_by,
            "err": (p.stderr or p.stdout)[:300] if not ok else ""}


def dep_files(blast_data):
    """Production dependent files from a blast payload: direct + indirect +
    the structural buckets (subclasses/composition/includes — the litellm
    lesson: inheritance dependents ARE dependents)."""
    files = []
    buckets = ("direct_callers", "affected_subclasses",
               "affected_via_composition", "affected_via_includes")
    for b in buckets:
        for c in blast_data.get(b) or []:
            f = c.get("file")
            if f:
                files.append(f)
    for c in blast_data.get("indirect_callers") or []:
        ref = c.get("ref") or ""
        f = ref.rsplit(":", 1)[0] if ":" in ref else ref
        if f:
            files.append(f)
    seen, out = set(), []
    for f in files:
        if f not in seen and not TEST_PATH.search(f):
            seen.add(f)
            out.append(f)
    return out


def read_texts(clone, files):
    texts = {}
    for f in files:
        try:
            texts[f] = open(os.path.join(clone, f), encoding="utf-8",
                            errors="replace").read()
        except OSError:
            texts[f] = ""
    return texts


def harvest_accessors(clone, contract_file, symbol):
    """Django-law harvest (pretix): related_name values declared in the
    contract's own file + the lowercase/plural conventions."""
    tokens = set()
    base = symbol.split(".")[-1].split("::")[-1]
    low = base.lower()
    tokens.update({f".{low}s", f".{low}_set"})
    if contract_file:
        try:
            text = open(os.path.join(clone, contract_file), encoding="utf-8",
                        errors="replace").read()
            for m in re.finditer(r'related_name\s*=\s*["\']([A-Za-z_]\w*)["\']', text):
                tokens.add("." + m.group(1))
        except OSError:
            pass
    return sorted(tokens)


def rg_files(clone, pattern, fixed=False):
    """Non-test prod files matching pattern repo-wide (rg; grep fallback).
    Explicit '.' path — rg with no path reads stdin. Returns None on error."""
    cmds = [["rg", "-l", "--no-messages"] + (["-F"] if fixed else []) + [pattern, "."]]
    if fixed:
        cmds.append(["grep", "-rlF", pattern, "."])
    for cmd in cmds:
        try:
            p = subprocess.run(cmd, cwd=clone, capture_output=True, text=True,
                               stdin=subprocess.DEVNULL)
        except OSError:
            continue
        if p.returncode in (0, 1):
            return [h[2:] if h.startswith("./") else h
                    for h in p.stdout.splitlines()
                    if h and not TEST_PATH.search(h)]
    return None


def battery(clone, symbol, contract_file, files, texts):
    """Covering-pattern battery. Per pattern: COVER (share of deps it matches)
    and PRECISION (deps ÷ all repo files it matches). A pattern is USABLE as a
    baseline enumerator only when it both covers (≥0.8) and is precise enough
    to hand over mostly-deps (≥0.3) — the haystack lesson: 'Document' covers
    everything and enumerates nothing (5435 hits), the import line does both."""
    base = symbol.split(".")[-1].split("::")[-1]
    fileset = set(files)
    results = []

    def add(name, kind, regex, fixed=False):
        rx = re.compile(regex if not fixed else re.escape(regex))
        in_deps = sum(1 for f in files if rx.search(texts[f]))
        row_hits = rg_files(clone, regex, fixed=fixed)
        row = {"pattern": name, "kind": kind,
               "cover": round(in_deps / len(files), 3) if files else 0.0}
        if row_hits is not None:
            inter = sum(1 for h in row_hits if h in fileset)
            row["repo_hits"] = len(row_hits)
            row["precision"] = round(inter / len(row_hits), 3) if row_hits else None
        row["usable"] = row["cover"] >= COVER_THRESHOLD and \
            (row.get("precision") is None or row["precision"] >= PRECISION_FLOOR)
        results.append(row)

    add(base, "token", regex=base, fixed=True)
    add(f"import …{base}", "import",
        regex=r"^\s*(from\s+\S+\s+import\b[^\n]*\b" + re.escape(base) +
              r"\b|import\s+[^\n]*\b" + re.escape(base) +
              r"\b|require[^\n]*" + re.escape(base.lower()) + r")")
    add(f"class X({base}) / < {base}", "subclass",
        regex=r"class\s+\w+\s*\([^)]*\b" + re.escape(base) + r"\b|<\s*" + re.escape(base) + r"\b")
    add(f"field: {base}", "annotation",
        regex=r'\w+\s*:\s*["\']?' + re.escape(base) + r'["\']?\b')
    for acc in harvest_accessors(clone, contract_file, symbol):
        add(acc, "accessor", regex=acc, fixed=True)

    from collections import Counter
    basenames = Counter(os.path.basename(f) for f in files)
    if basenames:
        top, n = basenames.most_common(1)[0]
        all_same = subprocess.run(["find", ".", "-name", top, "-type", "f"],
                                  cwd=clone, capture_output=True, text=True,
                                  stdin=subprocess.DEVNULL).stdout.splitlines()
        all_same = [f for f in all_same if not TEST_PATH.search(f)]
        prec = round(n / len(all_same), 3) if all_same else None
        results.append({"pattern": f"layout {top}", "kind": "layout",
                        "cover": round(n / len(files), 3),
                        "repo_hits": len(all_same), "precision": prec,
                        "usable": (n / len(files)) >= COVER_THRESHOLD and
                                  (prec or 0) >= PRECISION_FLOOR})
    return sorted(results, key=lambda r: -r["cover"])


def measure(clone, symbol, file_hint, sense):
    b03 = run_blast(clone, sense, symbol, 0.3, file_hint)
    b07 = run_blast(clone, sense, symbol, 0.7, file_hint)
    out = {"clone": clone, "symbol": symbol, "file_hint": file_hint,
           "bar4": {"at_0.3": {"ok": b03["ok"], "bytes": b03["bytes"],
                               "within_budget": b03["bytes"] <= BUDGET_BYTES,
                               "err": b03["err"]},
                    "at_0.7": {"ok": b07["ok"], "bytes": b07["bytes"],
                               "within_budget": b07["bytes"] <= BUDGET_BYTES,
                               "err": b07["err"]}}}
    if not b03["ok"]:
        out["verdict_hint"] = "BAR 4 FAIL (anchor unresolved at default confidence) — product finding first, admission verdict second: file it"
        return out

    direct03 = len(b03["data"].get("direct_callers") or [])
    g = run_graph_callers(clone, sense, symbol, file_hint)
    collapse = g["ok"] and g["called_by"] <= GRAPH_FOLD_FLOOR \
        and direct03 >= GRAPH_FOLD_RATIO * max(g["called_by"], 1)
    out["bar4"]["graph"] = {"ok": g["ok"], "called_by": g["called_by"],
                            "blast_direct_callers": direct03,
                            "fold_collapse": collapse,
                            "err": g["err"]}
    if collapse:
        out["verdict_hint"] = (
            f"BAR 4 FAIL (graph fold-collapse: called_by={g['called_by']} vs "
            f"blast direct={direct03} — the #191 signature; blast is blind to it) "
            "— product finding first, admission verdict second: file it")
        return out

    def grep_hit_files(base):
        """Non-test production files repo-wide containing the token (rg, with
        grep fallback). The denominator of seam_hunt-style precision."""
        # Explicit "." path: rg with no path searches STDIN, which silently
        # swallows a caller's heredoc/pipe. DEVNULL belt-and-suspenders.
        for cmd in (["rg", "-l", "--no-messages", base, "."],
                    ["grep", "-rl", base, "."]):
            p = subprocess.run(cmd, cwd=clone, capture_output=True, text=True,
                               stdin=subprocess.DEVNULL)
            if p.returncode in (0, 1):
                hits = [h for h in p.stdout.splitlines()
                        if h and not TEST_PATH.search(h)]
                return len(hits)
        return -1

    d = b03["data"]
    # CLI blast reports symbol as a string; the contract's defining file comes
    # from the --file hint or a graph lookup.
    contract_file = file_hint or ""
    if not contract_file:
        g = subprocess.run([sense, "graph", symbol, "--json"], cwd=clone,
                           capture_output=True, text=True,
                           stdin=subprocess.DEVNULL)
        try:
            contract_file = (json.loads(g.stdout).get("symbol") or {}).get("file", "")
        except ValueError:
            contract_file = ""
    files = dep_files(d)
    texts = read_texts(clone, files)
    base = symbol.split(".")[-1].split("::")[-1]
    invisible = [f for f in files if base not in texts[f]]
    dirs = sorted({"/".join(f.split("/")[:2]) for f in files})
    deps07 = dep_files(b07["data"]) if b07["ok"] else []

    hits = grep_hit_files(base)
    out["bar2"] = {"prod_dependent_files": len(files),
                   "grep_invisible": len(invisible),
                   "invisible_ratio": round(len(invisible) / len(files), 3) if files else 0.0,
                   "invisible_files": invisible[:20],
                   "grep_hit_files": hits,
                   "precision": round(len(files) / hits, 4) if hits > 0 else None,
                   "scatter_dirs": len(dirs),
                   "total_affected": d.get("total_affected", 0),
                   "survive_at_0.7": len(deps07)}
    bat = battery(clone, symbol, contract_file, files, texts)
    usable = [r for r in bat if r.get("usable")]
    best = (usable or bat or [{"cover": 0.0, "pattern": "-", "kind": "-"}])[0]
    out["bar3"] = {"battery": bat[:8],
                   "best_cover": best["cover"],
                   "best_pattern": f'{best["pattern"]} ({best["kind"]})',
                   "covered": bool(usable)}
    return out


def slot_verdict(out):
    """Composite verdict, calibrated on the 8-cell frozen backtest 2026-07-11.
    Kill rules (each anchored to a known non-win) route a candidate to the
    ballast lane; the win signature is what all three banked wins share.
    Documented residual: wagtail-class candidates (measurement-identical to a
    win, killed only by gold-level analysis) come out WIN-VIABLE here and die
    in the Loop 3 scout dig — the gate is the coarse sieve, not Event A."""
    b2, b3 = out["bar2"], out["bar3"]
    bat = b3["battery"]
    tok = next((r for r in bat if r["kind"] == "token"), {})
    kills = []
    if b3["covered"]:
        kills.append(f"K1 usable covering pattern ({b3['best_pattern']}) — healthchecks class")
    if b2["total_affected"] < 250 or b2["scatter_dirs"] <= 1:
        kills.append(f"K2 low volume/colocated (affected={b2['total_affected']}, "
                     f"scatter={b2['scatter_dirs']}) — pretix class")
    if any(r["kind"] == "subclass" and (r.get("precision") or 0) >= 0.7
           and r["cover"] >= 0.6 for r in bat):
        kills.append("K3 high-precision declared hierarchy → mechanized-enumeration "
                     "risk (ast.walk closes any depth) — litellm class")
    if b2["invisible_ratio"] < 0.05 and tok.get("cover", 0) >= 0.95 \
            and b2["prod_dependent_files"] < 30:
        kills.append(f"K4 seam-thin (invisible {b2['invisible_ratio']}, token cover "
                     f"{tok.get('cover')}, deps {b2['prod_dependent_files']}) — haystack class")
    if kills:
        return "BALLAST-ONLY", kills
    prec = tok.get("precision")
    if prec is not None and prec <= 0.3 and b2["total_affected"] >= 500:
        return "WIN-VIABLE", [f"win signature: no usable cover, token precision {prec}, "
                              f"affected {b2['total_affected']} (sentry/netbox/saleor class; "
                              f"wagtail-class false positives die in the scout dig)"]
    return "GRAY", ["measurements do not separate — Event A judgment, numbers recorded"]


def render(m):
    L = [f"### Admission gate — `{m['symbol']}` in {m['clone']}"]
    b4 = m["bar4"]
    for k in ("at_0.3", "at_0.7"):
        r = b4[k]
        L.append(f"- bar 4 {k}: ok={r['ok']} bytes={r['bytes']} "
                 f"within_budget={r['within_budget']}"
                 + (f" err={r['err']}" if r["err"] else ""))
    if "graph" in b4:
        g = b4["graph"]
        L.append(f"- bar 4 graph: ok={g['ok']} called_by={g['called_by']} "
                 f"blast_direct={g['blast_direct_callers']} "
                 f"fold_collapse={g['fold_collapse']}"
                 + (f" err={g['err']}" if g["err"] else ""))
    if "bar2" not in m:
        L.append(f"- **{m.get('verdict_hint', 'anchor unresolved')}**")
        return "\n".join(L)
    b2, b3 = m["bar2"], m["bar3"]
    L.append(f"- bar 2: prod dependents={b2['prod_dependent_files']} "
             f"grep-invisible={b2['grep_invisible']} "
             f"(ratio {b2['invisible_ratio']}) "
             f"grep_hits={b2['grep_hit_files']} precision={b2['precision']} "
             f"scatter_dirs={b2['scatter_dirs']} "
             f"total_affected={b2['total_affected']} "
             f"survive@0.7={b2['survive_at_0.7']}")
    L.append(f"- bar 3: {'COVERED (usable pattern: ' + b3['best_pattern'] + ', cover ' + str(b3['best_cover']) + ')' if b3['covered'] else 'no USABLE covering pattern (cover>=' + str(COVER_THRESHOLD) + ' AND precision>=' + str(PRECISION_FLOOR) + ')'}")
    for row in b3["battery"][:6]:
        prec = row.get("precision")
        L.append(f"    - cover={row['cover']:.2f} prec={prec if prec is not None else '  - '} "
                 f"hits={row.get('repo_hits', '-'):>5} {'USABLE' if row.get('usable') else '      '} "
                 f"{row['kind']:10s} {row['pattern']}")
    verdict, reasons = slot_verdict(m)
    L.append(f"- **slot verdict (mechanical half): {verdict}**")
    for r in reasons:
        L.append(f"    - {r}")
    L.append("- bar 1 (contract quality), bar 5 (memorization), bar 6 (slot+pillars), "
             "bar 7 (channel+banner): human checklist — judgment, not measured here")
    return "\n".join(L)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("clone")
    ap.add_argument("symbol")
    ap.add_argument("--file", dest="file_hint", default=None)
    ap.add_argument("--sense", default=os.environ.get("SENSE_BIN", "sense"))
    ap.add_argument("--json", dest="json_out", default=None)
    args = ap.parse_args()

    m = measure(args.clone, args.symbol, args.file_hint, args.sense)
    print(render(m))
    if args.json_out:
        with open(args.json_out, "w") as f:
            json.dump(m, f, indent=2)
    return 0


if __name__ == "__main__":
    sys.exit(main())
