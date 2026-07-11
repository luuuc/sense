#!/usr/bin/env python3
"""Loop 1 evaluator — mechanical bootstrap validation for a stamped vertical.

    bootstrap_check.py <key> --lang <extract-dir> [--prev <key>] [--stale tok1,tok2]

Four file-level checks (the Loop 1 un-fakeable list,
.doc/launch/00-next-vertical/loops/01-vertical-bootstrap.md); nothing here can
be gamed by prose:

  structure   the stamped tree has every element the previous vertical's
              template has (doc side + bench side)                     [FAIL]
  stale-refs  previous-stack tokens in PROMPT files (--stale). At STAMP time a
              hit = not-yet-retargeted content and should be ~zero (--strict
              makes hits fail); on a finished vertical residual mentions are
              legitimate comparisons, so hits print as a WARN worklist for the
              human prompt-refresh review                              [WARN]
  carry-forward  ../carry-forward.md has no unchecked items ("when this file is
              empty, the next vertical is fully bootstrapped")
  extractor   internal/extract/<lang>/ exists and is non-trivial

Exit 0 = bootstrapped; 1 = findings printed as a worklist. Run it against the
frozen python-django stamp as the fixture: structure/extractor PASS, and it
must honestly FAIL carry-forward while staged items remain.
"""

import argparse
import os
import re
import sys

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

DOC_ELEMENTS = ["README.md", "repos.md", "scenario-crafting.md", "prompts",
                "articles", "articles-plan.md"]
BENCH_ELEMENTS = ["scenarios"]


def find_doc_dir(key):
    launch = os.path.join(REPO, ".doc", "launch")
    for d in sorted(os.listdir(launch)):
        if d.endswith(f"-{key}-vertical"):
            return os.path.join(launch, d)
    return None


def check_structure(key, findings):
    doc = find_doc_dir(key)
    if not doc:
        findings.append(f"structure: no .doc/launch/NN-{key}-vertical/ directory")
        return
    for el in DOC_ELEMENTS:
        if not os.path.exists(os.path.join(doc, el)):
            findings.append(f"structure: missing {os.path.relpath(os.path.join(doc, el), REPO)}")
    bench = os.path.join(REPO, "bench", "verticals", key)
    if not os.path.isdir(bench):
        findings.append(f"structure: missing bench/verticals/{key}/")
        return
    for el in BENCH_ELEMENTS:
        if not os.path.exists(os.path.join(bench, el)):
            findings.append(f"structure: missing bench/verticals/{key}/{el}")


def check_stale_refs(key, stale, warns):
    doc = find_doc_dir(key)
    prompts = os.path.join(doc, "prompts") if doc else None
    if not prompts or not os.path.isdir(prompts):
        return  # structure check already reports it
    pats = [(t, re.compile(r"\b" + re.escape(t) + r"\b", re.IGNORECASE))
            for t in stale if t]
    for fn in sorted(os.listdir(prompts)):
        path = os.path.join(prompts, fn)
        if not os.path.isfile(path):
            continue
        text = open(path, encoding="utf-8", errors="replace").read()
        for tok, rx in pats:
            n = len(rx.findall(text))
            if n:
                warns.append(f"stale-refs: prompts/{fn}: {n}× '{tok}'")


def check_carry_forward(findings):
    cf = os.path.join(REPO, ".doc", "launch", "00-next-vertical", "carry-forward.md")
    if not os.path.exists(cf):
        findings.append("carry-forward: file missing")
        return
    unchecked = [l.strip() for l in open(cf, encoding="utf-8")
                 if re.match(r"\s*[-*]\s*\[ \]", l)]
    for item in unchecked:
        findings.append(f"carry-forward: unchecked: {item[:100]}")


def check_extractor(lang, findings):
    d = os.path.join(REPO, "internal", "extract", lang)
    if not os.path.isdir(d):
        findings.append(f"extractor: internal/extract/{lang}/ does not exist")
        return
    go_files = [f for f in os.listdir(d) if f.endswith(".go") and not f.endswith("_test.go")]
    if not go_files:
        findings.append(f"extractor: internal/extract/{lang}/ has no production files")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("key", help="vertical key, e.g. go, python-django")
    ap.add_argument("--lang", required=True, help="internal/extract/<lang> dir name")
    ap.add_argument("--prev", default=None, help="previous vertical key (informational)")
    ap.add_argument("--stale", default="", help="comma-separated stale stack tokens to grep prompts for")
    ap.add_argument("--strict", action="store_true",
                    help="stamp-time mode: stale-ref hits fail (prompts should be retargeted)")
    args = ap.parse_args()

    findings, warns = [], []
    check_structure(args.key, findings)
    check_stale_refs(args.key, args.stale.split(","), warns)
    check_carry_forward(findings)
    check_extractor(args.lang, findings)

    print(f"# bootstrap check — {args.key} (lang={args.lang})")
    for f in findings:
        print("FAIL:", f)
    for w in warns:
        print("WARN:" if not args.strict else "FAIL:", w)
    bad = len(findings) + (len(warns) if args.strict else 0)
    if bad:
        print(f"NOT BOOTSTRAPPED — {bad} finding(s)"
              + (f", {len(warns)} stale-ref warn(s) for the prompt-refresh review"
                 if warns and not args.strict else ""))
        return 1
    if warns:
        print(f"BOOTSTRAPPED with {len(warns)} stale-ref warn(s) — review at prompt-refresh")
        return 0
    print("BOOTSTRAPPED — structure, stale-refs, carry-forward, extractor all clean")
    return 0


if __name__ == "__main__":
    sys.exit(main())
