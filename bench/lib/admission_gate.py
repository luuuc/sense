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
          (b) grep-noise / PRECISION: dependents grep actually finds ÷
              repo-wide prod files containing the token (seam_hunt v3's
              win-pattern: LOWER precision = the baseline cannot cleanly
              enumerate the set). Intersection numerator, like the battery
              rows — counting grep-INVISIBLE deps in the numerator emitted
              values >1 (the hunt-v2 versionSet/ChannelTrace/indexMap
              anomaly, 2026-07-13).
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
          VERIFIED-EDGE FLOOR (G-3 recount, 2026-07-13): the ratio runs on
          blast direct @0.7 (the verified band), not @0.3 — tiny internal
          types ride the 0.3 bare-name band to the 60-cap and faked 24 of
          the hunt-v2 fires while their verified callers were honestly 0-4.
          A real collapse loses VERIFIED callers (#191: the healed edges
          came back at conf 0.8-1.0), so @0.7 stays numerous.

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
GRAPH_FOLD_RATIO = 10          # ...when blast direct@0.7 >= 10× it (collapse=60×, healthy max=1.08×)
SURVIVOR_FLOOR = 5             # dep files at 0.7 below this = gold impossible (blast-both-confidences
                               # law needs 8-10 gold files; banked wins 22-68, fabricated class <=3)
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


def dep_files(blast_data, exclude=None):
    """Production dependent files from a blast payload: direct + indirect +
    the structural buckets (subclasses/composition/includes — the litellm
    lesson: inheritance dependents ARE dependents).

    `exclude` is an optional compiled regex; matching paths are dropped (the
    gold-law scout use: a repo's in-tree example/docs app inflates the win
    signature with non-shippable callers (filament `docs-assets/`, 51 of 120
    Field callers). It filters the same dependent-file set the win metrics
    derive from, so invisibility/precision/scatter/survive_at_0.7 all re-derive
    on the gold-eligible side."""
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
        if f not in seen and not TEST_PATH.search(f) \
                and not (exclude and exclude.search(f)):
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


def go_interface_methods(clone, contract_file, base):
    """Method names declared by a Go interface `type <base> interface { ... }`.

    THE GO-SATISFACTION LAW (traefik Traceable, 2026-07-20): Go has no
    `implements` keyword, so a baseline never greps the INTERFACE name to find
    implementors, it greps a METHOD name. Traceable measured 31 prod
    dependents, 30 of them grep-invisible on the interface token (4 repo hits)
    and no usable covering pattern; one `grep -rl GetTracingInformation`
    returned 32 files = cover 1.0, precision 0.97. Without this row the battery
    scores single-interface anchors as seams when one grep enumerates them.
    Multi-interface retention rings are unaffected (dolt's paid-win gold spans
    5+ interfaces; the best single method grep covers 3 of its 12 files), which
    is exactly the discrimination we want the gate to make.
    """
    if not contract_file or not contract_file.endswith(".go"):
        return []
    try:
        with open(os.path.join(clone, contract_file), encoding="utf-8",
                  errors="replace") as fh:
            text = fh.read()
    except OSError:
        return []
    m = re.search(r"type\s+" + re.escape(base) + r"\s+interface\s*\{", text)
    if not m:
        return []
    depth, i = 1, m.end()
    while i < len(text) and depth:
        depth += (text[i] == "{") - (text[i] == "}")
        i += 1
    body = text[m.end():i - 1]
    methods = []
    for line in body.splitlines():
        line = line.split("//")[0].strip()
        sig = re.match(r"([A-Z]\w*)\s*\(", line)   # exported methods only
        if sig:
            methods.append(sig.group(1))
    return methods


def battery(clone, symbol, contract_file, files, texts):
    """Covering-pattern battery. Per pattern: COVER (share of deps it matches)
    and PRECISION (deps ÷ all repo files it matches). A pattern is USABLE as a
    baseline enumerator only when it both covers (≥0.8) and is precise enough
    to hand over mostly-deps (≥0.3) — the haystack lesson: 'Document' covers
    everything and enumerates nothing (5435 hits), the import line does both."""
    base = symbol.split(".")[-1].split("::")[-1]
    fileset = set(files)
    results = []
    matched = {}  # kind -> set of dep files the pattern matched (for the union flag)

    def add(name, kind, regex, fixed=False):
        rx = re.compile(regex if not fixed else re.escape(regex))
        hit_deps = {f for f in files if rx.search(texts[f])}
        matched.setdefault(kind, set()).update(hit_deps)
        in_deps = len(hit_deps)
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
    # Type-named accessor family (the GO-NAMING LAW, dolt dry-run kill 2026-07-13):
    # in Go, accessor/provider/constructor names INHERIT the type name
    # (dEnv.DoltDB(, GetDoltDB(, DoltDatabases(), NewBatch() — all contain the
    # type substring in a CALL position), so "holders of X" is one substring
    # sweep even in files that never name the type. Precision does NOT protect
    # this pattern: receiver-verifying the hits is window-resolvable (THE
    # BATCHING LAW), so slot_verdict kills on cover alone (K6).
    add(f".*{base}*( call", "type-accessor",
        regex=r"[.\t (]\w*" + re.escape(base) + r"\w*\s*\(")
    # GO-SATISFACTION: implementors are found by the interface's METHOD name,
    # never by the interface name (Go has no `implements`). Precision-eligible:
    # a rare method name both covers and enumerates, so K1 kills on it.
    iface_methods = go_interface_methods(clone, contract_file, base)
    if iface_methods:
        add("go-iface-method " + "/".join(iface_methods[:3]), "go-iface-method",
            regex=r"[.\t (]\b(" + "|".join(re.escape(x) for x in iface_methods) +
                  r")\s*\(")
    add(f"import …{base}", "import",
        regex=r"(?m)^\s*(from\s+\S+\s+import\b[^\n]*\b" + re.escape(base) +
              r"\b|import\s+[^\n]*\b" + re.escape(base) +
              r"\b|require[^\n]*" + re.escape(base.lower()) + r")")
    add(f"class X({base}) / < {base}", "subclass",
        regex=r"class\s+\w+\s*\([^)]*\b" + re.escape(base) + r"\b|<\s*" + re.escape(base) + r"\b")
    # PKG-IMPORT (the import law): in Go the dependents of a symbol are almost
    # always importers of its defining PACKAGE, so one
    # `grep -rl '"<module>/<pkgdir>'` enumerates a superset, and file-level
    # cited credit accepts ANY line pin, so the dump scores full recall
    # (measured probe: a 366-file dump = cited_recall 1.00 against a 30-file
    # gold; scored with gold.score_gold_recall). Precision does NOT protect it
    # (0.08 there): the kill is cover alone (K7 in slot_verdict), so `usable`
    # stays False here to keep K1's precision-floor semantics. Quoted-prefix
    # needle: subpackage importers count; in-package deps need no import line
    # and are counted covered. Root-package anchors match exact (`"<module>"`);
    # a bare-module prefix would match every internal import.
    gomod = os.path.join(clone, "go.mod")
    if os.path.exists(gomod):
        mod = re.search(r"^module\s+(\S+)", open(gomod).read(), re.M)
        pkgdir = os.path.dirname(contract_file) if contract_file else ""
        if mod:
            needle = '"' + mod.group(1) + ("/" + pkgdir if pkgdir else '"')
            in_pkg = {f for f in files
                      if os.path.dirname(f) == (pkgdir or "")}
            hit_deps = {f for f in files if needle in texts[f]} | in_pkg
            matched.setdefault("pkg-import", set()).update(hit_deps)
            row = {"pattern": f"import {needle}", "kind": "pkg-import",
                   "cover": round(len(hit_deps) / len(files), 3) if files else 0.0}
            row_hits = rg_files(clone, needle, fixed=True)
            if row_hits is not None:
                hits_all = set(row_hits) | in_pkg
                inter = sum(1 for h in hits_all if h in fileset)
                row["repo_hits"] = len(hits_all)
                row["precision"] = round(inter / len(hits_all), 3) if hits_all else None
            row["usable"] = False
            results.append(row)
    # PHP-USE (the import law, PHP form): the generic import row above is
    # python/go/ruby-shaped and scores 0.00 on PHP, which would blind bar 3 to
    # PHP's strongest covering pattern. Two adversaries: (a) per-class
    # `use <NS>\<Class>;` (incl. `as` aliases), a normal precision-eligible
    # row; (b) the namespace-prefix dump `use <NS>\`, an importer SUPERSET
    # like Go's pkg-import, so `usable` stays False and the kill is cover
    # alone (K7). In-namespace deps declare no use line and are counted
    # covered via same-dir membership, mirroring the Go in-pkg rule.
    if os.path.exists(os.path.join(clone, "composer.json")):
        add(f"use …\\{base};", "php-use",
            regex=r"(?m)^\s*use\s+[\w\\]+\\" + re.escape(base) + r"\s*(;|\s+as\s)")
        ns = None
        if contract_file:
            try:
                with open(os.path.join(clone, contract_file), encoding="utf-8",
                          errors="replace") as fh:
                    m = re.search(r"^namespace\s+([\w\\]+)\s*;", fh.read(), re.M)
                    ns = m.group(1) if m else None
            except OSError:
                ns = None
        if ns:
            needle = "use " + ns + "\\"
            nsdir = os.path.dirname(contract_file)
            in_ns = {f for f in files if os.path.dirname(f) == nsdir}
            hit_deps = {f for f in files if needle in texts[f]} | in_ns
            matched.setdefault("php-ns-use", set()).update(hit_deps)
            row = {"pattern": needle, "kind": "php-ns-use",
                   "cover": round(len(hit_deps) / len(files), 3) if files else 0.0}
            row_hits = rg_files(clone, needle, fixed=True)
            if row_hits is not None:
                hits_all = set(row_hits) | in_ns
                inter = sum(1 for h in hits_all if h in fileset)
                row["repo_hits"] = len(hits_all)
                row["precision"] = round(inter / len(hits_all), 3) if hits_all else None
            row["usable"] = False
            results.append(row)
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
    # NAME-FAMILY UNION (dolt dry-run lesson 2026-07-13): the adversary COMPOSES
    # the type-token grep (param/field holders name the type) with the
    # type-named-accessor sweep (token-free holders call an accessor that
    # inherits the name). Neither pattern alone need cover; the union does.
    # Not a kill (sentry's token cover is ~1.0 and it's a banked WIN — noise +
    # whole-structure judgment defeat the batcher) — a PROBE-REQUIRED flag.
    union = matched.get("token", set()) | matched.get("type-accessor", set())
    union_cover = round(len(union) / len(files), 3) if files else 0.0
    return sorted(results, key=lambda r: -r["cover"]), union_cover


def measure(clone, symbol, file_hint, sense, exclude=None):
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
    direct07 = len(b07["data"].get("direct_callers") or []) if b07["ok"] else 0
    g = run_graph_callers(clone, sense, symbol, file_hint)
    collapse = g["ok"] and g["called_by"] <= GRAPH_FOLD_FLOOR \
        and direct07 >= GRAPH_FOLD_RATIO * max(g["called_by"], 1)
    out["bar4"]["graph"] = {"ok": g["ok"], "called_by": g["called_by"],
                            "blast_direct_callers": direct03,
                            "blast_direct_at_0.7": direct07,
                            "fold_collapse": collapse,
                            "err": g["err"]}
    if collapse:
        out["verdict_hint"] = (
            f"BAR 4 FAIL (graph fold-collapse: called_by={g['called_by']} vs "
            f"blast direct@0.7={direct07} verified — the #191 signature; "
            "blast is blind to it) "
            "— product finding first, admission verdict second: file it")
        return out

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
    files = dep_files(d, exclude)
    texts = read_texts(clone, files)
    base = symbol.split(".")[-1].split("::")[-1]
    invisible = [f for f in files if base not in texts[f]]
    # Scatter counts distinct dirs at depth 2 AFTER stripping the deps' common
    # directory prefix: raw depth-2 collapses package monorepos (bagisto
    # `packages/Webkul/*`, laravel-framework `src/Illuminate/*`) to one "dir"
    # and over-fires K2's colocated arm. Prefix-stripping only refines counts
    # upward; the K2 volume arm is untouched.
    parts = [f.split("/")[:-1] for f in files]
    common = os.path.commonprefix(parts) if parts else []
    dirs = sorted({"/".join(f.split("/")[len(common):][:2]) or "." for f in files})
    deps07 = dep_files(b07["data"], exclude) if b07["ok"] else []

    hit_list = rg_files(clone, base, fixed=True)
    hits = len(hit_list) if hit_list is not None else -1
    grep_found = sum(1 for h in hit_list or [] if h in set(files))
    # Retained ring size (K7's exemption): retention gold lives OUTSIDE the
    # importer set (pebble Batch banked win; grpc-go Attributes 55 import-proof
    # files), so an import-superset kill is only sound when this escape is
    # below the survivor floor.
    ret_files = {(r.get("file") or r.get("ref", "").split(":")[0])
                 for r in (d.get("retained_via_interfaces") or [])
                 if isinstance(r, dict)}
    ret_prod = {f for f in ret_files if f and not TEST_PATH.search(f)}
    out["bar2"] = {"prod_dependent_files": len(files),
                   "retained_prod_files": len(ret_prod),
                   "grep_invisible": len(invisible),
                   "invisible_ratio": round(len(invisible) / len(files), 3) if files else 0.0,
                   "invisible_files": invisible[:20],
                   "grep_hit_files": hits,
                   "precision": round(grep_found / hits, 4) if hits > 0 else None,
                   "scatter_dirs": len(dirs),
                   "total_affected": d.get("total_affected", 0),
                   "total_affected_raw": bool(exclude),
                   "survive_at_0.7": len(deps07)}
    if exclude:
        out["bar2"]["excluded_pattern"] = exclude.pattern
        out["exclude_note"] = ("file-derived metrics (invisibility, precision, "
                               "scatter, survive_at_0.7) are docs/example-excluded; "
                               "total_affected is the raw transitive blast scalar")
    bat, union_cover = battery(clone, symbol, contract_file, files, texts)
    usable = [r for r in bat if r.get("usable")]
    best = (usable or bat or [{"cover": 0.0, "pattern": "-", "kind": "-"}])[0]
    out["bar3"] = {"battery": bat[:8],
                   "best_cover": best["cover"],
                   "best_pattern": f'{best["pattern"]} ({best["kind"]})',
                   "covered": bool(usable),
                   "name_family_union_cover": union_cover}
    return out


def slot_verdict(out):
    """Composite verdict, calibrated on the 8-cell frozen backtest 2026-07-11.
    Kill rules (each anchored to a known non-win) route a candidate to the
    ballast lane; the win signature is what all three banked wins share.
    Documented residual: wagtail-class candidates (measurement-identical to a
    win, killed only by gold-level analysis) come out WIN-VIABLE here and die
    in the Loop 3 scout dig — the gate is the coarse sieve, not Event A."""
    b2, b3 = out["bar2"], out["bar3"]
    g = out["bar4"].get("graph", {})
    if b2["total_affected"] == 0 and g.get("called_by", 0) == 0:
        return "MEASUREMENT-INVALID", [
            "zero edges everywhere (blast affected=0, graph called_by=0) — check index "
            "health (`sense status`) before reading this cell; the litellm 2026-07-13 "
            "broken-index (schema v0, 0 edges) measured exactly this shape"]
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
    if b2["survive_at_0.7"] < SURVIVOR_FLOOR:
        kills.append(f"K5 verified-band starved (survive@0.7={b2['survive_at_0.7']}, "
                     f"called_by={g.get('called_by')}) — gold impossible under the "
                     "blast-both-confidences law; the seam is 0.3-band fabrication "
                     "(zapRaftLogger/noCopy class, hunt v3 2026-07-13)")
    ta = next((r for r in bat if r["kind"] == "type-accessor"), {})
    if ta.get("cover", 0) >= COVER_THRESHOLD:
        kills.append(f"K6 type-named accessor family covers the deps (cover "
                     f"{ta['cover']}, {ta.get('repo_hits', '?')} repo hits) — the "
                     "GO-NAMING LAW; noise does not protect it, receiver checks are "
                     "window-resolvable (dolt DoltDB dry-run class, 2026-07-13)")
    pi = next((r for r in bat if r["kind"] == "pkg-import"), {})
    if pi.get("cover", 0) >= COVER_THRESHOLD \
            and b2.get("retained_prod_files", 0) < SURVIVOR_FLOOR:
        kills.append(f"K7 defining-package import superset covers the deps (cover "
                     f"{pi['cover']}, {pi.get('repo_hits', '?')} repo hits) and the "
                     f"retained ring offers no escape "
                     f"({b2.get('retained_prod_files', 0)} prod files < {SURVIVOR_FLOOR}) "
                     "- the IMPORT LAW; precision does not protect it, file-level "
                     "cited credit accepts any line pin (probe: a 366-file dump "
                     "scored 1.00; retention exemption = the pebble Batch "
                     "banked-win backtest)")
    nu = next((r for r in bat if r["kind"] == "php-ns-use"), {})
    if nu.get("cover", 0) >= COVER_THRESHOLD \
            and b2.get("retained_prod_files", 0) < SURVIVOR_FLOOR:
        kills.append(f"K7 namespace use-prefix superset covers the deps (cover "
                     f"{nu['cover']}, {nu.get('repo_hits', '?')} repo hits) and the "
                     f"retained ring offers no escape "
                     f"({b2.get('retained_prod_files', 0)} prod files < {SURVIVOR_FLOOR}) "
                     "- the IMPORT LAW in PHP clothing (`use <NS>\\` dump)")
    if kills:
        return "BALLAST-ONLY", kills
    prec = tok.get("precision")
    notes = []
    if b3.get("name_family_union_cover", 0) >= COVER_THRESHOLD:
        notes.append(f"⚠ NAME-FAMILY UNION cover {b3['name_family_union_cover']} "
                     "(token ∪ type-accessor) — dolt-class composition risk; the "
                     "ADVERSARY PROBE (manifesto §8 step 3b) is REQUIRED before any "
                     "gold curation on this anchor")
    if prec is not None and prec <= 0.3 and b2["total_affected"] >= 500:
        return "WIN-VIABLE", [f"win signature: no usable cover, token precision {prec}, "
                              f"affected {b2['total_affected']} (sentry/netbox/saleor class; "
                              f"wagtail-class false positives die in the scout dig)"] + notes
    return "GRAY", ["measurements do not separate — Event A judgment, numbers recorded"] + notes


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
                 f"blast_direct@0.7={g['blast_direct_at_0.7']} "
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
    ap.add_argument("--exclude", default=None,
                    help="regex; dependent files whose path matches are dropped "
                         "from the win-signature metrics (gold-law scout: strip "
                         "in-tree example/docs apps, e.g. '^docs-assets/')")
    args = ap.parse_args()

    exclude = re.compile(args.exclude) if args.exclude else None
    m = measure(args.clone, args.symbol, args.file_hint, args.sense, exclude)
    print(render(m))
    if args.json_out:
        with open(args.json_out, "w") as f:
            json.dump(m, f, indent=2)
    return 0


if __name__ == "__main__":
    sys.exit(main())
