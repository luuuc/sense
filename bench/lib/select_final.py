#!/usr/bin/env python3
"""select_final.py -- pick the final-benchmark runs by PROVENANCE, not folder position.

"The last two runs of the release are the final data" is a query over run_meta, not a
folder-position rule: a stray re-run landing as run-99 would poison a position rule.
Per (arm, repo) this selects the latest two VALID runs that qualify for the target
release, and prints what it selected AND what it rejected and why (no silent drops).

Qualification:
  - valid == true                          (else: reject "invalid")
  - sense arm: sense_release == <target>   (else: reject "not-release")
               AND sense_dirty == false    (else: reject "dirty-tree" -- a build can
               sit on a tag with a dirty tree; that is not the release)
  - baseline arm: version-independent (the bare clone); any valid run qualifies
  - among qualifiers, the latest two by timestamp
  - warns if the runs selected for a repo do not share one scenario_version
    (measurement drift: they scored different scenarios)

  python3 bench/lib/select_final.py <release-tag> [--results DIR]

Reads run_meta.json under RESULTS_DIR (or --results). The provenance lives in the
run, so path layout is irrelevant -- model-scoped or model-less, both are found.
Read-only, $0.
"""
import glob
import json
import os
import sys


def _load_metas(root):
    metas = []
    for path in glob.glob(os.path.join(root, "**", "run_meta.json"), recursive=True):
        try:
            with open(path) as f:
                m = json.load(f)
        except (OSError, ValueError):
            continue
        m["_dir"] = os.path.dirname(path)
        metas.append(m)
    return metas


def _reject_reason(m, release):
    """Why this run does NOT qualify for the release, or None if it qualifies."""
    if not m.get("valid"):
        return "invalid"
    if m.get("tool") == "sense":
        if m.get("sense_release") != release:
            return "not-release"
        if m.get("sense_dirty"):
            return "dirty-tree"
    return None


def select(metas, release):
    """Return {(tool, repo): {"selected": [...], "rejected": [(m, reason)]}}."""
    groups = {}
    for m in metas:
        groups.setdefault((m.get("tool"), m.get("repo")), []).append(m)
    out = {}
    for key, runs in groups.items():
        qualifiers, rejected = [], []
        for m in runs:
            reason = _reject_reason(m, release)
            (rejected.append((m, reason)) if reason else qualifiers.append(m))
        # latest two by timestamp (missing timestamp sorts oldest)
        qualifiers.sort(key=lambda m: m.get("timestamp") or "", reverse=True)
        out[key] = {"selected": qualifiers[:2], "rejected": rejected}
    return out


def _rel(root, d):
    return os.path.relpath(d, root) if d else "?"


def report(result, root):
    lines, warnings = [], []
    # group the flat (tool, repo) result by repo so drift is visible per repo
    by_repo = {}
    for (tool, repo), r in sorted(result.items()):
        by_repo.setdefault(repo, {})[tool] = r
    for repo, arms in sorted(by_repo.items()):
        lines.append(f"\n{repo}")
        versions = set()
        for tool, r in sorted(arms.items()):
            sel = r["selected"]
            versions.update(m.get("scenario_version") for m in sel)
            picked = ", ".join(_rel(root, m["_dir"]) for m in sel) or "(none qualify)"
            lines.append(f"  {tool:<9} selected {len(sel)}: {picked}")
            for m, reason in r["rejected"]:
                lines.append(f"  {tool:<9} rejected {_rel(root, m['_dir'])}: {reason}")
            if len(sel) < 2:
                warnings.append(f"{repo}/{tool}: only {len(sel)} qualifying run(s), need 2")
        versions.discard(None)
        if len(versions) > 1:
            warnings.append(f"{repo}: selected runs span {len(versions)} scenario_versions "
                            f"(measurement drift): {sorted(versions)}")
    return "\n".join(lines), warnings


def _default_root():
    if os.environ.get("RESULTS_DIR"):
        return os.environ["RESULTS_DIR"]
    return os.path.normpath(os.path.join(os.path.dirname(__file__), "..", "results"))


def _parse(argv):
    """-> (release, root). --results DIR overrides RESULTS_DIR/default."""
    release, root = None, None
    it = iter(argv[1:])
    for a in it:
        if a == "--results":
            root = next(it, None)
        elif release is None:
            release = a
    if not release:
        sys.exit("usage: select_final.py <release-tag> [--results DIR]")
    return release, root or _default_root()


def main(argv):
    release, root = _parse(argv)
    metas = _load_metas(root)
    if not metas:
        sys.exit(f"no run_meta.json found under {root}")
    body, warnings = report(select(metas, release), root)
    print(f"final-benchmark selection for release {release} (root: {root})")
    print(body)
    if warnings:
        print("\nWARNINGS:")
        for w in warnings:
            print(f"  ! {w}")
    return 1 if warnings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
