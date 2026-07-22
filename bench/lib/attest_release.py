#!/usr/bin/env python3
"""attest_release.py -- record a maintainer attestation of which Sense release an
already-finished run used.

Some runners never stamped Sense provenance: opencode-run.sh wrote no sense_* fields
at all, and codex-run.sh recorded `sense_release: null` with a dirty tree. Those runs
are unverifiable from their own record, so select_final rejects every one of them as
"not-release" -- which silently drops whole cross-model arms from the final selection.

The maintainer knows which binary was installed. This writes that fact INTO the run
record as an explicit attestation, never as if the runner had stamped it: the run keeps
whatever it actually observed, and gains

    sense_release          the attested tag
    sense_release_source   "maintainer-attestation-<date>"  <- the tell that it is asserted
    sense_release_note     why the runner could not stamp it

A run that stamped its own release is NEVER touched, so a genuine older-binary run (an
opus pre-replay run on v1.12.4) cannot be rewritten into the new release by this tool.

Usage:
  attest_release.py <results-root> <tag> --source <who-and-when> [--arm sense]
                    [--models a,b] [--waive-dirty] [--apply]

Defaults to a DRY RUN; nothing is written without --apply.
"""
import argparse
import glob
import json
import os
import sys


def candidates(root, arm, models):
    """Run dirs of the given arm that carry NO self-stamped release."""
    out = []
    for path in sorted(glob.glob(os.path.join(root, "*", arm, "*", "run-*", "run_meta.json"))):
        model_dir = os.path.relpath(path, root).split(os.sep)[0]
        if models and model_dir not in models:
            continue
        with open(path) as f:
            meta = json.load(f)
        # Self-stamped runs are authoritative and must never be overwritten.
        if meta.get("sense_release") and not meta.get("sense_release_source"):
            continue
        out.append((path, meta))
    return out


def attest(meta, tag, source, note, waive_dirty):
    """Return the meta with the attestation applied (does not write)."""
    meta["sense_release"] = tag
    meta["sense_release_source"] = source
    meta["sense_release_note"] = note
    if waive_dirty and meta.get("sense_dirty"):
        # sense_dirty is a proxy for "the binary may not be the release". The
        # attestation speaks to the binary directly, so record the waiver rather
        # than editing the observation.
        meta["sense_dirty_waived"] = True
    return meta


def main(argv):
    ap = argparse.ArgumentParser()
    ap.add_argument("root")
    ap.add_argument("tag")
    ap.add_argument("--source", required=True)
    ap.add_argument("--note", default="runner recorded no Sense release; attested after the fact")
    ap.add_argument("--arm", default="sense")
    ap.add_argument("--models", default="")
    ap.add_argument("--waive-dirty", action="store_true")
    ap.add_argument("--apply", action="store_true")
    a = ap.parse_args(argv[1:])

    models = [m for m in a.models.split(",") if m]
    found = candidates(a.root, a.arm, models)
    for path, meta in found:
        attest(meta, a.tag, a.source, a.note, a.waive_dirty)
        rel = os.path.relpath(os.path.dirname(path), a.root)
        if a.apply:
            with open(path, "w") as f:
                json.dump(meta, f, indent=2)
        print(f"{'wrote ' if a.apply else 'would attest '}{rel} -> {a.tag}")
    print(f"{len(found)} run(s){'' if a.apply else ' (DRY RUN, pass --apply)'}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
