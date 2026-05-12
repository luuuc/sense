#!/usr/bin/env python3
"""Validate scenario changes and detect regressions.

Pre-run mode:  validate-changes.py --original-dir DIR --improved-dir DIR [--backup-dir DIR]
Post-run mode: validate-changes.py --original-dir DIR --improved-dir DIR --new-scores DIR --old-scores PATH
"""

import argparse
import json
import os
import shutil
import sys

BENCH2_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
sys.path.insert(0, os.path.join(BENCH2_DIR, "lib"))

from scenario import parse as parse_scenario, validate_scenario
import yaml


def pre_run_validate(original_dir, improved_dir, backup_dir=None):
    errors = []
    warnings = []

    improved_files = [f for f in os.listdir(improved_dir) if f.endswith(".yaml")]
    if not improved_files:
        errors.append("No improved YAML files found")
        return {"valid": False, "errors": errors, "warnings": warnings}

    for fname in improved_files:
        improved_path = os.path.join(improved_dir, fname)
        original_path = os.path.join(original_dir, fname)

        try:
            with open(improved_path) as f:
                data = yaml.safe_load(f)
            validate_scenario(data)
        except Exception as e:
            errors.append(f"{fname}: YAML validation failed: {e}")
            continue

        weights = data.get("scoring", {}).get("weights", {})
        total = sum(weights.values())
        if abs(total - 1.0) > 0.01:
            errors.append(f"{fname}: weights sum to {total}, expected 1.0")

        if os.path.exists(original_path):
            try:
                with open(original_path) as f:
                    orig_data = yaml.safe_load(f)
            except Exception:
                continue

            for si, step in enumerate(orig_data.get("steps", [])):
                if si >= len(data.get("steps", [])):
                    errors.append(f"{fname}: step {si} '{step.get('name','')}' removed entirely")
                    continue
                new_step = data["steps"][si]
                orig_required_count = sum(
                    1 for c in step.get("checks", []) if c.get("required", True)
                )
                new_required_count = sum(
                    1 for c in new_step.get("checks", []) if c.get("required", True)
                )
                if new_required_count < orig_required_count:
                    errors.append(
                        f"{fname}: step {si} required checks decreased "
                        f"from {orig_required_count} to {new_required_count}"
                    )
                new_total = len(new_step.get("checks", []))
                orig_total = len(step.get("checks", []))
                if new_total < orig_total:
                    warnings.append(
                        f"{fname}: step {si} total checks decreased "
                        f"from {orig_total} to {new_total}"
                    )

    if backup_dir and not errors:
        os.makedirs(backup_dir, exist_ok=True)
        for fname in os.listdir(original_dir):
            if fname.endswith(".yaml"):
                shutil.copy2(
                    os.path.join(original_dir, fname),
                    os.path.join(backup_dir, fname),
                )
        warnings.append(f"Backup created at {backup_dir}")

    return {
        "valid": len(errors) == 0,
        "errors": errors,
        "warnings": warnings,
        "files_validated": len(improved_files),
        "backup_path": backup_dir if backup_dir and not errors else None,
    }


def post_run_regression(old_scores_path, new_scores_dir, gap_tolerance=0.05, abs_tolerance=0.15, changed_repos=None):
    with open(old_scores_path) as f:
        old_analysis = json.load(f)

    regressions = []
    comparisons = {}

    for repo, data in old_analysis.get("repos", {}).items():
        old_sense = data["current_scores"].get("sense")
        old_baseline = data["current_scores"].get("baseline")
        if old_sense is None or old_baseline is None:
            continue
        old_gap = old_sense - old_baseline

        new_sense_path = os.path.join(new_scores_dir, "sense", repo, "scored.json")
        new_baseline_path = os.path.join(new_scores_dir, "baseline", repo, "scored.json")

        if not os.path.exists(new_sense_path) or not os.path.exists(new_baseline_path):
            continue

        with open(new_sense_path) as f:
            new_sense = json.load(f).get("overall_score", 0)
        with open(new_baseline_path) as f:
            new_baseline = json.load(f).get("overall_score", 0)
        new_gap = new_sense - new_baseline

        comparisons[repo] = {
            "old_sense": old_sense,
            "old_baseline": old_baseline,
            "old_gap": round(old_gap, 4),
            "new_sense": new_sense,
            "new_baseline": new_baseline,
            "new_gap": round(new_gap, 4),
            "gap_change": round(new_gap - old_gap, 4),
        }

        is_changed = changed_repos is None or repo in changed_repos
        if is_changed and new_gap < old_gap - gap_tolerance:
            regressions.append({
                "repo": repo,
                "type": "gap_regression",
                "old_gap": round(old_gap, 4),
                "new_gap": round(new_gap, 4),
            })
        sense_drop = old_sense - new_sense
        if is_changed and sense_drop > abs_tolerance:
            regressions.append({
                "repo": repo,
                "type": "absolute_regression",
                "old_sense": old_sense,
                "new_sense": new_sense,
                "drop": round(sense_drop, 4),
            })

    return {
        "regressed": len(regressions) > 0,
        "regressions": regressions,
        "comparisons": comparisons,
        "action": "rollback" if regressions else "apply",
    }


def main():
    parser = argparse.ArgumentParser(description="Validate scenario changes")
    parser.add_argument("--original-dir", required=True)
    parser.add_argument("--improved-dir", required=True)
    parser.add_argument("--backup-dir", default=None)
    parser.add_argument("--old-scores", default=None, help="Path to analysis.json from before changes")
    parser.add_argument("--new-scores", default=None, help="Path to results/ dir after re-run")
    parser.add_argument("--output", default=None)
    parser.add_argument("--changed-repos", default=None, help="Comma-separated list of repos that were changed (only these are checked for regressions)")
    args = parser.parse_args()

    if args.new_scores and args.old_scores:
        changed = set(args.changed_repos.split(",")) if args.changed_repos else None
        result = post_run_regression(args.old_scores, args.new_scores, changed_repos=changed)
    else:
        result = pre_run_validate(args.original_dir, args.improved_dir, args.backup_dir)

    output = json.dumps(result, indent=2)
    if args.output:
        os.makedirs(os.path.dirname(args.output), exist_ok=True)
        with open(args.output, "w") as f:
            f.write(output)
            f.write("\n")
        print(f"Validation written to {args.output}", file=sys.stderr)
    else:
        print(output)


if __name__ == "__main__":
    main()
