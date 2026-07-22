#!/usr/bin/env python3
"""select_final.py -- pick the final-benchmark runs by PROVENANCE, not folder position.

"The last two runs of the release are the final data" is a query over run_meta, not a
folder-position rule: a stray re-run landing as run-99 would poison a position rule.
Per (MODEL, arm, repo) this selects the latest two runs that MEASURED the arm and
qualify for the target release, and prints what it selected AND what it rejected
and why (no silent drops). Grouping without the model compared arms across
models, which measures the model gap rather than Sense.

Qualification:
  - the run measured the arm                (else: reject with its artifact class)
    Derived per run by run_validity.classify_run, NOT read off run_meta.valid: a
    run the wall clock cut short is a failed exam, not a void one, and the stored
    flag said otherwise until 2026-07-21.
  - sense arm: sense_release == <target>   (else: reject "not-release")
               AND sense_dirty == false    (else: reject "dirty-tree" -- a build can
               sit on a tag with a dirty tree; that is not the release), UNLESS
               sense_dirty_waived is set by a maintainer attestation
               (lib/attest_release.py). An attested release always carries
               sense_release_source, so an asserted tag stays distinguishable
               from one the runner stamped itself.
  - baseline arm: version-independent (the bare clone); any valid run qualifies
  - among qualifiers, the latest two by timestamp
  - warns if one model's selected runs do not share a scenario_version
    (measurement drift: its two arms scored different scenarios)
  - RUNS=2 binds the arm named by --headline; other models are confirmation
    arms, run x1 by design, and are flagged OPEN instead of short

  python3 bench/lib/select_final.py <release-tag> [--results DIR] [--headline MODEL]

Reads run_meta.json under RESULTS_DIR (or --results). The provenance lives in the
run, so path layout is irrelevant -- model-scoped or model-less, both are found.
Read-only, $0.
"""
import glob
import json
import os
import sys

import run_validity


def _load_metas(root):
    metas = []
    for path in glob.glob(os.path.join(root, "**", "run_meta.json"), recursive=True):
        try:
            with open(path) as f:
                m = json.load(f)
        except (OSError, ValueError):
            continue
        run_dir = os.path.dirname(path)
        if _quarantined(root, run_dir):
            continue
        m["_dir"] = run_dir
        m["_class"] = run_validity.classify_run(m, _load_scored(run_dir))
        metas.append(m)
    return metas


def _quarantined(root, run_dir):
    """True for runs that are on disk but not on the published board.

    Parked (`_voided-*`), superseded (`failed-run-*`) and probe (`dryruns-*`)
    runs. These were invisible while everything was rejected as invalid; once
    validity is derived they would otherwise become selectable, which is exactly
    backwards -- `baseline/dolt/failed-run-2-claude-session` sits beside the
    run-2 that replaced it, so selecting it double-counts the cell.
    """
    return run_validity.is_parked(os.path.relpath(run_dir, root))


def _load_scored(run_dir):
    """The run's scored.json, which carries the answer evidence run_meta lacks."""
    try:
        with open(os.path.join(run_dir, "scored.json")) as f:
            return json.load(f)
    except (OSError, ValueError):
        return {}


def _reject_reason(m, release):
    """Why this run does NOT qualify for the release, or None if it qualifies.

    Validity is DERIVED from what the run left behind (run_validity.classify_run),
    not read off the stored `valid` flag: the flag was `rc == 0` until 2026-07-21,
    which voided every run the wall clock cut short. Deriving reclassifies the
    already-paid-for runs with no rewrite of the recorded artifacts.
    """
    cls = m.get("_class") or run_validity.classify_run(m, _load_scored(m.get("_dir", "")))
    if not cls["valid"]:
        return cls["void_reason"]
    if m.get("tool") == "sense":
        if m.get("sense_release") != release:
            return "not-release"
        # sense_dirty is a PROXY for "the binary might not be the release".
        # A maintainer attestation speaks to the binary directly, so an
        # explicitly waived dirty flag does not reject (lib/attest_release.py).
        if m.get("sense_dirty") and not m.get("sense_dirty_waived"):
            return "dirty-tree"
    return None


def select(metas, release):
    """Return {(model, tool, repo): {"selected": [...], "rejected": [...]}}.

    Grouped by MODEL as well as arm and repo. Grouping by (tool, repo) alone
    compared arms across models: consul's "final" pick was two claude-fable-5
    sense runs against a gpt-5.5 and a claude-opus-4-8 baseline, which measures
    the model gap, not Sense.
    """
    groups = {}
    for m in metas:
        groups.setdefault((_model(m), m.get("tool"), m.get("repo")), []).append(m)
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


def _model(m):
    """The arm's model id, or "?" when the runner recorded none."""
    return m.get("model") or "?"


def _rel(root, d):
    return os.path.relpath(d, root) if d else "?"


def report(result, root, headline=None):
    lines, warnings = [], []
    # group by repo, then model, so drift is visible per repo and every
    # comparison printed is same-model
    by_repo = {}
    for (model, tool, repo), r in sorted(result.items()):
        by_repo.setdefault(repo, {}).setdefault(model, {})[tool] = r
    for repo, models in sorted(by_repo.items()):
        lines.append(f"\n{repo}")
        for model, arms in sorted(models.items()):
            lines.append(f"  [{model}]")
            # Drift is per MODEL: two arms of one model scoring different
            # scenario versions is what breaks a comparison. Two models on
            # different versions is not a drift, they are separate rows.
            versions = set()
            for tool, r in sorted(arms.items()):
                sel = r["selected"]
                versions.update(m.get("scenario_version") for m in sel)
                picked = ", ".join(_rel(root, m["_dir"]) for m in sel) or "(none qualify)"
                lines.append(f"    {tool:<9} selected {len(sel)}: {picked}")
                for m, reason in r["rejected"]:
                    lines.append(f"    {tool:<9} rejected {_rel(root, m['_dir'])}: {reason}")
                # RUNS=2 binds the HEADLINE arm only. A cross-model confirmation
                # arm runs x1 by design and carries an OPEN flag instead
                # (00-vertical-bench-manifesto.md, Prime Directive 6).
                if len(sel) < 2:
                    if headline is None or model == headline:
                        warnings.append(
                            f"{repo}/{model}/{tool}: only {len(sel)} qualifying run(s), need 2")
                    elif sel:
                        warnings.append(
                            f"{repo}/{model}/{tool}: n=1, OPEN flag (directional confirmation)")
                    else:
                        warnings.append(f"{repo}/{model}/{tool}: no qualifying run")
            versions.discard(None)
            if len(versions) > 1:
                warnings.append(
                    f"{repo}/{model}: selected runs span {len(versions)} scenario_versions "
                    f"(measurement drift): {sorted(versions)}")
    return "\n".join(lines), warnings


def _default_root():
    if os.environ.get("RESULTS_DIR"):
        return os.environ["RESULTS_DIR"]
    return os.path.normpath(os.path.join(os.path.dirname(__file__), "..", "results"))


def _parse(argv):
    """-> (release, root, headline). --results DIR overrides RESULTS_DIR/default."""
    release, root, headline = None, None, None
    it = iter(argv[1:])
    for a in it:
        if a == "--results":
            root = next(it, None)
        elif a == "--headline":
            headline = next(it, None)
        elif release is None:
            release = a
    if not release:
        sys.exit("usage: select_final.py <release-tag> [--results DIR] [--headline MODEL]")
    return release, root or _default_root(), headline


def main(argv):
    release, root, headline = _parse(argv)
    metas = _load_metas(root)
    if not metas:
        sys.exit(f"no run_meta.json found under {root}")
    body, warnings = report(select(metas, release), root, headline)
    print(f"final-benchmark selection for release {release} (root: {root})")
    print(body)
    if warnings:
        print("\nWARNINGS:")
        for w in warnings:
            print(f"  ! {w}")
    return 1 if warnings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
