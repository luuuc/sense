#!/usr/bin/env python3
"""Apply structured improvements to scenario YAML files.

Usage: generate-improvements.py --improvements PATH --scenarios-dir DIR --output-dir DIR

Reads an improvements.json file (generated during analysis) and applies
the specified changes to scenario YAMLs. Writes modified YAMLs + changes manifest.
"""

import argparse
import json
import os
import sys

import yaml

BENCH2_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
sys.path.insert(0, os.path.join(BENCH2_DIR, "lib"))


FORBIDDEN_FAIRNESS_TYPES = {"mcp_tool_used", "no_grep"}


def validate_improvements(improvements):
    """Strip forbidden check types from fairness layer. Returns (cleaned, warnings)."""
    warnings = []
    cleaned = {"scenarios": []}
    for scenario_imp in improvements.get("scenarios", []):
        cleaned_mods = []
        for mod in scenario_imp.get("modifications", []):
            action = mod.get("action", "")
            if action in ("add_check", "tighten_check"):
                new_check = mod.get("new_check", {})
                check_type = new_check.get("type", "")
                check_layer = new_check.get("layer", "fairness")
                if check_type in FORBIDDEN_FAIRNESS_TYPES and check_layer != "adoption":
                    warnings.append(
                        f"BLOCKED {scenario_imp['repo']}: "
                        f"{action} with type={check_type} forbidden in fairness layer"
                    )
                    continue
            cleaned_mods.append(mod)
        if cleaned_mods:
            cleaned["scenarios"].append({**scenario_imp, "modifications": cleaned_mods})
    return cleaned, warnings


def apply_improvements(improvements, scenarios_dir, output_dir):
    os.makedirs(output_dir, exist_ok=True)
    changes_manifest = {}

    for scenario_improvement in improvements.get("scenarios", []):
        repo = scenario_improvement["repo"]
        scenario_file = os.path.join(scenarios_dir, f"{repo}.yaml")
        if not os.path.exists(scenario_file):
            print(f"SKIP: {scenario_file} not found", file=sys.stderr)
            continue

        with open(scenario_file) as f:
            scenario = yaml.safe_load(f)

        changes = {
            "checks_tightened": 0,
            "checks_added": 0,
            "checks_removed": 0,
            "weights_changed": False,
            "details": [],
        }

        for mod in scenario_improvement.get("modifications", []):
            action = mod["action"]
            step_idx = mod.get("step_idx")

            if action == "tighten_check":
                old_check = mod["old_check"]
                new_check = mod["new_check"]
                step = scenario["steps"][step_idx]
                for ci, check in enumerate(step["checks"]):
                    if check["type"] == old_check["type"] and check["value"] == old_check["value"]:
                        step["checks"][ci] = {**check, **new_check}
                        changes["checks_tightened"] += 1
                        changes["details"].append(
                            f"step {step_idx}: {old_check['type']}={old_check['value']} → "
                            f"{new_check['type']}={new_check['value']}"
                        )
                        break

            elif action == "add_check":
                new_check = mod["new_check"]
                scenario["steps"][step_idx]["checks"].append(new_check)
                changes["checks_added"] += 1
                changes["details"].append(
                    f"step {step_idx}: added {new_check['type']}={new_check['value']}"
                )

            elif action == "promote_to_required":
                check_type = mod["check_type"]
                check_value = mod["check_value"]
                step = scenario["steps"][step_idx]
                for check in step["checks"]:
                    if check["type"] == check_type and check["value"] == check_value:
                        check["required"] = True
                        changes["details"].append(
                            f"step {step_idx}: {check_type}={check_value} → required"
                        )
                        break

            elif action == "update_weights":
                scenario["scoring"]["weights"] = mod["weights"]
                changes["weights_changed"] = True
                changes["details"].append(f"weights → {mod['weights']}")

            elif action == "remove_check":
                check_type = mod["check_type"]
                check_value = mod["check_value"]
                step = scenario["steps"][step_idx]
                original_len = len(step["checks"])
                step["checks"] = [
                    c for c in step["checks"]
                    if not (c["type"] == check_type and c["value"] == check_value)
                ]
                removed = original_len - len(step["checks"])
                if removed > 0:
                    changes["checks_removed"] += 1
                    changes["details"].append(
                        f"step {step_idx}: removed {check_type}={check_value}"
                    )

            elif action == "raise_threshold":
                check_type = mod["check_type"]
                old_value = mod["old_value"]
                new_value = mod["new_value"]
                step = scenario["steps"][step_idx]
                for check in step["checks"]:
                    if check["type"] == check_type and check["value"] == old_value:
                        check["value"] = new_value
                        changes["checks_tightened"] += 1
                        changes["details"].append(
                            f"step {step_idx}: {check_type} threshold {old_value} → {new_value}"
                        )
                        break

        output_path = os.path.join(output_dir, f"{repo}.yaml")
        with open(output_path, "w") as f:
            yaml.dump(scenario, f, default_flow_style=False, sort_keys=False, allow_unicode=True)

        changes_manifest[f"{repo}.yaml"] = changes
        print(f"Wrote {output_path}", file=sys.stderr)

    return changes_manifest


def main():
    parser = argparse.ArgumentParser(description="Apply improvements to scenario YAMLs")
    parser.add_argument("--improvements", required=True, help="Path to improvements.json")
    parser.add_argument("--scenarios-dir", default=os.path.join(BENCH2_DIR, "scenarios"))
    parser.add_argument("--output-dir", required=True, help="Directory for modified YAMLs")
    args = parser.parse_args()

    with open(args.improvements) as f:
        improvements = json.load(f)

    improvements, validation_warnings = validate_improvements(improvements)
    for w in validation_warnings:
        print(f"WARNING: {w}", file=sys.stderr)

    manifest = apply_improvements(improvements, args.scenarios_dir, args.output_dir)

    print(json.dumps(manifest, indent=2))


if __name__ == "__main__":
    main()
