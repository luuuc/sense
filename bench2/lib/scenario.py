#!/usr/bin/env python3
"""Parse and validate scenario YAML files.

A scenario is a realistic, multi-step developer session:
  1. Explore  → find callers, trace data flow, understand structure
  2. Analyze  → assess impact, locate tests, identify risks
  3. Modify   → apply a change (add parameter, extract method, etc.)
  4. Verify   → confirm the change compiles/tests pass

Usage: scenario.py <scenario.yaml>  →  emits parsed JSON to stdout.
"""

import json
import re
import sys

import yaml


CHECK_TYPES = {"contains", "exact", "diff_contains", "transcript_contains",
                "word", "phrase", "starts_with", "mcp_tool_used", "no_grep",
                "response_richness"}

SCENARIO_REQUIRED = {
    "name": str,
    "repo": str,
    "description": str,
    "steps": list,
}

SCENARIO_OPTIONAL = {
    "repo_commit": str,
    "time_budget_minutes": int,
    "system_prompt": str,
    "scoring": dict,
}

STEP_REQUIRED = {
    "name": str,
    "prompt": str,
    "checks": list,
}

STEP_OPTIONAL = {
    "time_budget_minutes": int,
    "expected_domain": str,
    "expects_file_modifications": bool,
}

CHECK_REQUIRED = {
    "type": str,
    "value": str,
}

CHECK_OPTIONAL = {
    "required": bool,
    "description": str,
    "layer": str,
}


def _typecheck(value, expected_type, context):
    if not isinstance(value, expected_type):
        raise ValueError(f"{context}: expected {expected_type.__name__}, got {type(value).__name__}")


def validate_scenario(data):
    errors = []

    for key, typ in SCENARIO_REQUIRED.items():
        if key not in data:
            errors.append(f"missing required field: {key}")
        elif not isinstance(data[key], typ):
            errors.append(f"{key}: expected {typ.__name__}, got {type(data[key]).__name__}")

    for key, typ in SCENARIO_OPTIONAL.items():
        if key in data and not isinstance(data[key], typ):
            errors.append(f"{key}: expected {typ.__name__}, got {type(data[key]).__name__}")

    if "steps" in data and isinstance(data["steps"], list):
        for i, step in enumerate(data["steps"]):
            ctx = f"steps[{i}]"
            if not isinstance(step, dict):
                errors.append(f"{ctx}: expected dict")
                continue
            for key, typ in STEP_REQUIRED.items():
                if key not in step:
                    errors.append(f"{ctx}.{key}: missing required field")
                elif not isinstance(step[key], typ):
                    errors.append(f"{ctx}.{key}: expected {typ.__name__}")
            for key, typ in STEP_OPTIONAL.items():
                if key in step and not isinstance(step[key], typ):
                    errors.append(f"{ctx}.{key}: expected {typ.__name__}")

            if "checks" in step and isinstance(step["checks"], list):
                for j, check in enumerate(step["checks"]):
                    cctx = f"{ctx}.checks[{j}]"
                    if not isinstance(check, dict):
                        errors.append(f"{cctx}: expected dict")
                        continue
                    for key, typ in CHECK_REQUIRED.items():
                        if key not in check:
                            errors.append(f"{cctx}.{key}: missing required field")
                    if "type" in check:
                        if check["type"] not in CHECK_TYPES:
                            errors.append(f"{cctx}.type: must be one of {CHECK_TYPES}, got '{check['type']}'")
                    if "required" in check and not isinstance(check["required"], bool):
                        errors.append(f"{cctx}.required: expected bool")

    if "scoring" in data and isinstance(data["scoring"], dict):
        pass

    if errors:
        raise ValueError("\n".join(errors))

    return data


def parse(path):
    with open(path) as f:
        data = yaml.safe_load(f)
    validate_scenario(data)

    defaults = {"repo_commit": "", "time_budget_minutes": 12, "scoring": {}}
    for key, default in defaults.items():
        data.setdefault(key, default)

    scoring = data.get("scoring", {})
    scoring.setdefault("weights", {
        "correctness": 0.70,
        "efficiency": 0.30,
    })
    scoring.setdefault("metrics", [
        "token_input", "token_output", "wall_time", "tool_calls", "misses", "cost_usd",
    ])

    for step in data["steps"]:
        step.setdefault("time_budget_minutes", 3)
        step.setdefault("expects_file_modifications", False)
        for check in step.get("checks", []):
            check.setdefault("required", True)

    return data


def build_full_prompt(scenario):
    """Build the full scenario prompt text for a Claude session.

    Includes system-level instructions, step-by-step tasks, and response format
    expectations. All steps are presented as ONE continuous prompt so Claude
    works through them sequentially in a single session.
    """
    lines = []

    sys_prompt = scenario.get("system_prompt", "")
    if sys_prompt:
        lines.append(sys_prompt.strip())
        lines.append("")

    lines.append(f"## Scenario: {scenario['name']}")
    lines.append(f"Repository: {scenario['repo']}")
    lines.append(f"{scenario['description']}")
    lines.append("")
    lines.append("Complete each step below, in order. After finishing all steps,")
    lines.append("respond with a JSON summary of what you did and found.")
    lines.append("Do not use Explore agents or sub-agents.")
    lines.append("")

    for i, step in enumerate(scenario["steps"], 1):
        lines.append(f"### Step {i}: {step['name']}")
        lines.append("")
        lines.append(step["prompt"].strip())
        lines.append("")

    return "\n".join(lines)


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: scenario.py <scenario.yaml> [--json] [--prompt]", file=sys.stderr)
        sys.exit(1)

    scenario = parse(sys.argv[1])

    if "--json" in sys.argv:
        print(json.dumps(scenario, indent=2))
    elif "--prompt" in sys.argv:
        print(build_full_prompt(scenario))
    else:
        print(json.dumps(scenario, indent=2))
