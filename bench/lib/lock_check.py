"""Lock enforcement for the improvement loop.

Two responsibilities:

1. Validate that an `improvements.json` payload only targets tunables
   permitted by `bench/global/locked/locked.yaml`. Drops any modification that
   targets a locked entry, with a human-readable reason.

2. Verify the held-out set's integrity by comparing every file's SHA256
   against `bench/global/locked/held-out.lock`. Loop refuses to continue on
   mismatch (panic-class failure — the bench's anchor has been disturbed).

Both responsibilities are split into pure functions and a CLI:

    python3 bench/lib/lock_check.py --validate-improvements PATH
    python3 bench/lib/lock_check.py --check-held-out

Exit codes:
    0 = pass
    2 = held-out lockfile mismatch (panic — bench halts)
    3 = improvements payload contains forbidden entries (some dropped or all rejected)
    4 = config / I/O error
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from typing import Any

import yaml

BENCH_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
PROJECT_ROOT = os.path.abspath(os.path.join(BENCH_DIR, ".."))
LOCKED_YAML = os.path.join(BENCH_DIR, "global", "locked", "locked.yaml")


# --- Loading ----------------------------------------------------------------

def load_locked(path: str = LOCKED_YAML) -> dict[str, Any]:
    with open(path) as f:
        return yaml.safe_load(f)


# --- Improvements-payload validation ---------------------------------------

def validate_improvements(
    improvements: dict[str, Any],
    locked: dict[str, Any],
) -> tuple[dict[str, Any], list[str]]:
    """Drop forbidden modifications. Returns (cleaned_payload, rejection_reasons).

    Rules enforced:
    - Action must be in locked["permitted_actions"].
    - update_weights: weights map keys must be a subset of
      locked["fairness_formula_axes"] (no axis additions/removals).

    Held-out scenarios are NOT blocked here because they live under
    bench/scenarios/held-out/ and generate-improvements.py only writes
    to the top-level scenarios dir; a `repo: axum` improvement applies
    to bench/scenarios/axum.yaml, never to held-out/axum-towers.yaml.
    Held-out integrity is protected by check_held_out (SHA256 lockfile),
    which is the right tool for the job.
    """
    permitted_actions = set(locked.get("permitted_actions", []))
    fairness_axes = set(locked.get("fairness_formula_axes", []))

    cleaned = {"scenarios": []}
    reasons: list[str] = []

    for scenario in improvements.get("scenarios", []):
        repo = scenario.get("repo", "")
        cleaned_mods = []
        for mod in scenario.get("modifications", []):
            action = mod.get("action", "")
            if action not in permitted_actions:
                reasons.append(
                    f"DROP {repo}: action={action!r} not in permitted_actions"
                )
                continue
            if action == "update_weights":
                weights = mod.get("weights", {}) or {}
                stray = set(weights) - fairness_axes
                if stray:
                    reasons.append(
                        f"DROP {repo} update_weights: stray axes {sorted(stray)} "
                        f"(allowed: {sorted(fairness_axes)})"
                    )
                    continue
            cleaned_mods.append(mod)

        if cleaned_mods:
            cleaned["scenarios"].append({**scenario, "modifications": cleaned_mods})

    return cleaned, reasons


# --- Held-out integrity -----------------------------------------------------

def sha256_file(path: str) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def held_out_files(held_out_dir: str) -> list[str]:
    """Every file under held_out_dir (scenarios + rubrics + transcripts + gold).

    Returns paths relative to held_out_dir, sorted for deterministic hashing.
    """
    out: list[str] = []
    for root, dirs, files in os.walk(held_out_dir):
        # Stable iteration order
        dirs.sort()
        for name in sorted(files):
            if name == ".DS_Store" or name.endswith(".lock"):
                continue
            full = os.path.join(root, name)
            rel = os.path.relpath(full, held_out_dir)
            out.append(rel)
    return out


def compute_held_out_manifest(held_out_dir: str) -> dict[str, str]:
    """rel_path → sha256 for every file under held_out_dir."""
    manifest = {}
    for rel in held_out_files(held_out_dir):
        manifest[rel] = sha256_file(os.path.join(held_out_dir, rel))
    return manifest


def load_held_out_lockfile(path: str) -> dict[str, str]:
    with open(path) as f:
        return json.load(f)["files"]


def check_held_out(
    held_out_dir: str, lockfile_path: str
) -> tuple[bool, list[str]]:
    """Return (ok, mismatches). ok is False if any file differs or is missing."""
    if not os.path.exists(lockfile_path):
        return False, [f"lockfile missing: {lockfile_path}"]

    expected = load_held_out_lockfile(lockfile_path)
    actual = compute_held_out_manifest(held_out_dir)

    mismatches: list[str] = []
    for rel, exp_hash in expected.items():
        act_hash = actual.get(rel)
        if act_hash is None:
            mismatches.append(f"MISSING: {rel}")
        elif act_hash != exp_hash:
            mismatches.append(
                f"MODIFIED: {rel} (expected {exp_hash[:12]}, got {act_hash[:12]})"
            )

    extra = set(actual) - set(expected)
    for rel in sorted(extra):
        mismatches.append(f"UNTRACKED: {rel} (add to lockfile or remove)")

    return len(mismatches) == 0, mismatches


def write_held_out_lockfile(held_out_dir: str, lockfile_path: str) -> None:
    """Generate / regenerate the lockfile from the current held_out_dir state."""
    manifest = compute_held_out_manifest(held_out_dir)
    payload = {
        "schema_version": 1,
        "held_out_dir": os.path.relpath(held_out_dir, PROJECT_ROOT),
        "files": manifest,
    }
    os.makedirs(os.path.dirname(lockfile_path), exist_ok=True)
    with open(lockfile_path, "w") as f:
        json.dump(payload, f, indent=2, sort_keys=True)
        f.write("\n")


# --- CLI --------------------------------------------------------------------

def _cmd_validate_improvements(args) -> int:
    locked = load_locked()
    with open(args.path) as f:
        improvements = json.load(f)

    cleaned, reasons = validate_improvements(improvements, locked)

    if args.write_to:
        with open(args.write_to, "w") as f:
            json.dump(cleaned, f, indent=2)
            f.write("\n")

    for r in reasons:
        print(r, file=sys.stderr)

    if not cleaned["scenarios"]:
        print("ALL improvements rejected.", file=sys.stderr)
        return 3
    if reasons:
        print(
            f"Validated with {len(reasons)} rejection(s). "
            f"Cleaned payload retains {len(cleaned['scenarios'])} scenario(s).",
            file=sys.stderr,
        )
        return 3 if args.strict else 0
    print("All improvements pass lock_check.", file=sys.stderr)
    return 0


def _cmd_check_held_out(args) -> int:
    locked = load_locked()
    held_out_dir = os.path.join(PROJECT_ROOT, locked["held_out_dir"])
    lockfile = os.path.join(PROJECT_ROOT, locked["held_out_lockfile"])
    ok, mismatches = check_held_out(held_out_dir, lockfile)
    if ok:
        print(f"held-out integrity OK ({held_out_dir})", file=sys.stderr)
        return 0
    print("HELD-OUT INTEGRITY FAILURE — bench refuses to continue:", file=sys.stderr)
    for m in mismatches:
        print(f"  {m}", file=sys.stderr)
    return 2


def _cmd_write_lock(args) -> int:
    locked = load_locked()
    held_out_dir = os.path.join(PROJECT_ROOT, locked["held_out_dir"])
    lockfile = os.path.join(PROJECT_ROOT, locked["held_out_lockfile"])
    write_held_out_lockfile(held_out_dir, lockfile)
    n = len(load_held_out_lockfile(lockfile))
    print(f"wrote {lockfile} ({n} files)", file=sys.stderr)
    return 0


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = p.add_subparsers(dest="cmd", required=True)

    p_val = sub.add_parser("validate-improvements")
    p_val.add_argument("path", help="path to improvements.json")
    p_val.add_argument("--write-to", help="write cleaned payload here")
    p_val.add_argument(
        "--strict",
        action="store_true",
        help="exit non-zero if any modification was dropped",
    )
    p_val.set_defaults(func=_cmd_validate_improvements)

    p_chk = sub.add_parser("check-held-out")
    p_chk.set_defaults(func=_cmd_check_held_out)

    p_wl = sub.add_parser("write-lock")
    p_wl.set_defaults(func=_cmd_write_lock)

    args = p.parse_args()
    try:
        return args.func(args)
    except (OSError, yaml.YAMLError, json.JSONDecodeError, KeyError) as e:
        print(f"lock_check error: {e}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
