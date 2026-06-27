#!/usr/bin/env python3
"""Seam-hunt helper v3 — the scout step of the per-repo vertical loop (manifesto §8.3).

The win pattern (calibrated on chatwoot/lobsters/discourse, carried to Django) is
NOT binary grep-invisibility. It is two things together:
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

With --propose it also emits a CANDIDATE gold skeleton (the lift carry-forward
item asked for): the scattered dependent set ranked one-per-dir, ready for the
human to curate, fill `relation:`, prune to ~16 heterogeneous non-obvious deps,
and trim each `match` to a unique suffix. The proposal is ADVISORY; it never
authors the scenario (Loop B stays human-anchored — auto-improvement-endgame).

Language is auto-detected from the clone (Ruby/Rails or Python/Django); override
with --lang. The Ruby computation (grep roots, test/non-app filters, scatter) is
unchanged from v2; the header line now also prints the resolved lang.

Ambiguous central symbols (re-exported / name-collision across apps, e.g.
Django's ProductVariant in both graphql types and product/models.py) need
--file to disambiguate — the same hint the MCP blast/graph tools return
([[project_sense_blast_file_disambiguation]]). Pass the gold's contract_file.

Usage:
  python3 bench/lib/seam_hunt.py <clone_dir> <Symbol> [token] \
      [--hops N] [--conf F] [--lang ruby|python|auto] [--file PATH] [--propose]
"""
import json, os, subprocess, sys, collections

# Per-language scout config. Adding a stack = adding a row here. The Ruby row
# reproduces v2 exactly (grep roots app+lib, spec/test as tests, db/config as
# non-app), so existing Rails scouting is unchanged.
LANGS = {
    "ruby": {
        "include": "*.rb",
        "grep_roots": ["app", "lib"],
        "exclude_dirs": [],
        # f is a path relative to the clone root.
        "is_test": lambda f: f.startswith("spec/") or "/spec/" in f
        or f.startswith("test/") or "/test/" in f,
        "nonapp": lambda f: f.startswith("db/") or f.startswith("config/"),
    },
    "python": {
        "include": "*.py",
        "grep_roots": ["."],
        "exclude_dirs": [".git", ".venv", "venv", "node_modules", "build", "dist"],
        "is_test": lambda f: f.startswith("tests/") or "/tests/" in f
        or os.path.basename(f).startswith("test_") or f.endswith("_test.py")
        or "conftest" in os.path.basename(f) or "/testing/" in f,
        "nonapp": lambda f: "/migrations/" in f or f.startswith("docs/")
        or "/docs/" in f or os.path.basename(f) in ("setup.py", "conftest.py"),
    },
}


def run(cmd, cwd):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)


def detect_lang(clone):
    """Marker-file first (cheap, unambiguous), then a *.py-vs-*.rb file count."""
    if os.path.exists(os.path.join(clone, "Gemfile")):
        return "ruby"
    if any(os.path.exists(os.path.join(clone, m)) for m in
           ("manage.py", "pyproject.toml", "setup.py", "setup.cfg")):
        return "python"
    rb = run(["bash", "-c", "git ls-files '*.rb' | head -1"], clone).stdout.strip()
    py = run(["bash", "-c", "git ls-files '*.py' | head -1"], clone).stdout.strip()
    if py and not rb:
        return "python"
    return "ruby"  # default preserves v2 behavior for unknown trees


def collect_callers(clone, symbol, conf, hops, file_hint):
    """Union of blast direct/indirect callers and 2-deep graph callers."""
    file_flag = ["--file", file_hint] if file_hint else []
    callers = {}  # file -> set(caller symbols)
    bl = run(["sense", "blast", symbol, "--min-confidence", conf,
              "--max-hops", hops, "--json"] + file_flag, clone)
    try:
        d = json.loads(bl.stdout)
        for key in ("direct_callers", "indirect_callers"):
            for c in d.get(key, []):
                f = c.get("file")
                if f:
                    callers.setdefault(f, set()).add(c.get("symbol"))
    except Exception as e:
        print(f"[blast parse err] {e}", file=sys.stderr)
    gr = run(["sense", "graph", symbol, "--direction", "callers",
              "--depth", "2", "--json"] + file_flag, clone)
    try:
        d = json.loads(gr.stdout)
        for c in d.get("edges", {}).get("called_by", []):
            f = c.get("file")
            if f:
                callers.setdefault(f, set()).add(c.get("symbol"))
    except Exception as e:
        print(f"[graph parse err] {e}", file=sys.stderr)
    return callers


def grep_precision(clone, cfg, token, is_test):
    """Repo-wide grep-hit files for the token (app code only) — the noise gauge."""
    cmd = ["grep", "-rlw", "--include=" + cfg["include"]]
    for d in cfg["exclude_dirs"]:
        cmd.append("--exclude-dir=" + d)
    cmd += [token] + cfg["grep_roots"]
    gh = run(cmd, clone)
    return [x for x in gh.stdout.splitlines()
            if x and not is_test(x.lstrip("./"))]


def propose_gold(symbol, app_callers):
    """Emit a CANDIDATE gold skeleton: one dependent per dir first (max scatter),
    then the rest, each ready for the human to curate + fill `relation:`."""
    print("\n# ---- CANDIDATE gold skeleton (ADVISORY — curate before benching) ----")
    print("# Prune to ~16 heterogeneous, NON-OBVIOUS, scattered deps (manifesto §4).")
    print("# Fill each relation:, trim each match: to a UNIQUE file suffix, and")
    print("# verify uniqueness with `git ls-files | grep -c <suffix>`. The model")
    print("# drafts this into <repo>.yaml; the human reviews (Loop B is human-anchored).")
    print("gold:")
    print(f"  - {{id: contract, group: contract, match: [TODO-contract-file], "
          f"relation: \"{symbol}, the central abstraction under change\"}}")
    # one representative file per dir first, so the skeleton leads with scatter
    seen_dirs, ordered = set(), []
    for f in sorted(app_callers, key=lambda f: (os.path.dirname(f), f)):
        d = os.path.dirname(f)
        if d not in seen_dirs:
            seen_dirs.add(d)
            ordered.append(f)
    ordered += [f for f in sorted(app_callers) if f not in ordered]
    for f in ordered:
        segs = f.split("/")
        suffix = "/".join(segs[-2:]) if len(segs) >= 2 else f
        base = os.path.splitext(os.path.basename(f))[0]
        parent = segs[-2] if len(segs) >= 2 else "dep"
        did = f"dep:{parent}-{base}".replace("_", "-")
        syms = ", ".join(sorted(s for s in app_callers[f] if s))[:80]
        print(f"  - {{id: {did}, group: dependents, match: [{suffix}], "
              f"relation: \"TODO — {syms or symbol} reaches {symbol} how?\"}}")
    print("# ---- end candidate (do NOT paste verbatim; it is a starting point) ----")


def main():
    args = sys.argv[1:]
    if len(args) < 2:
        print(__doc__)
        sys.exit("usage: seam_hunt.py <clone_dir> <Symbol> [token] "
                 "[--hops N] [--conf F] [--lang L] [--propose]")
    clone = args[0]
    symbol = args[1]
    rest = args[2:]
    token = None
    hops = "3"
    conf = "0.3"
    lang = "auto"
    file_hint = None
    propose = False
    i = 0
    while i < len(rest):
        if rest[i] == "--hops":
            hops = rest[i + 1]; i += 2
        elif rest[i] == "--conf":
            conf = rest[i + 1]; i += 2
        elif rest[i] == "--lang":
            lang = rest[i + 1]; i += 2
        elif rest[i] == "--file":
            file_hint = rest[i + 1]; i += 2
        elif rest[i] == "--propose":
            propose = True; i += 1
        else:
            token = rest[i]; i += 1
    if token is None:
        token = symbol.split("#")[-1].split(".")[-1].split("::")[-1]
    if lang == "auto":
        lang = detect_lang(clone)
    cfg = LANGS.get(lang)
    if not cfg:
        sys.exit(f"unknown --lang '{lang}' (known: {', '.join(LANGS)})")

    callers = collect_callers(clone, symbol, conf, hops, file_hint)
    is_test = cfg["is_test"]
    is_app = lambda f: not (is_test(f) or cfg["nonapp"](f))
    app_callers = {f: s for f, s in callers.items() if is_app(f)}
    dirs = collections.Counter(os.path.dirname(f) for f in app_callers)

    grep_files = grep_precision(clone, cfg, token, is_test)
    grep_n = len(grep_files)
    prec = (len(app_callers) / grep_n) if grep_n else 0.0

    print(f"=== {symbol}  (lang={lang}, token='{token}', hops={hops}, conf={conf}) ===")
    print(f"  app caller files : {len(app_callers)}   (total incl test/non-app: {len(callers)})")
    print(f"  SCATTER (dirs)   : {len(dirs)}   -> {dict(dirs.most_common(8))}")
    print(f"  GREP-NOISE       : '{token}' in {grep_n} app files   PRECISION={prec:.2f}  (lower=grep-hostile)")
    verdict = []
    if len(dirs) >= 5:
        verdict.append("SCATTERED")
    else:
        verdict.append(f"colocated({len(dirs)}dir)")
    if grep_n and prec <= 0.5:
        verdict.append("GREP-NOISY")
    elif grep_n:
        verdict.append("grep-clean")
    if not app_callers:
        verdict.append("RESOLVER-GAP?")
    print(f"  VERDICT          : {' + '.join(verdict)}")
    print(f"  --- app callers ---")
    for f in sorted(app_callers):
        print(f"    {f}  <- {', '.join(sorted(s for s in app_callers[f] if s))[:70]}")

    if propose:
        propose_gold(symbol, app_callers)


if __name__ == "__main__":
    main()
