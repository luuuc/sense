#!/usr/bin/env python3
"""Parse a bench task YAML file and emit JSON for the runner.

Usage: parse_task.py <task.yaml> [repo_name]

Without repo_name: emits full task metadata (name, variables, repos list, scoring).
With repo_name: emits the rendered prompt and repo-specific params.
"""

import json
import sys

import yaml


def parse_task(path):
    with open(path) as f:
        data = yaml.safe_load(f)

    result = {}
    for key in ("name", "description"):
        if key in data:
            result[key] = data[key]

    result["variables"] = data.get("variables", [])
    result["prompt_template"] = data.get("prompt_template", "")
    result["repos"] = data.get("repos", {})

    scoring_raw = data.get("scoring", {})
    scoring = {}
    correctness = scoring_raw.get("correctness", {})
    for key in ("type", "match_key", "partial_credit", "rubric"):
        if key in correctness:
            scoring[key] = correctness[key]

    scoring["metrics"] = scoring_raw.get("metrics", [])
    result["scoring"] = scoring

    return result


def render_prompt(task, repo_name):
    template = task.get("prompt_template", "")
    repo_params = task.get("repos", {}).get(repo_name, {})
    prompt = template
    for var in task.get("variables", []):
        value = repo_params.get(var, f"{{{var}}}")
        prompt = prompt.replace(f"{{{var}}}", str(value))
    return prompt


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: parse_task.py <task.yaml> [repo_name]", file=sys.stderr)
        sys.exit(1)

    task = parse_task(sys.argv[1])

    if len(sys.argv) >= 3:
        repo_name = sys.argv[2]
        if repo_name not in task.get("repos", {}):
            print(json.dumps({"error": f"repo '{repo_name}' not in task"}))
            sys.exit(1)
        output = {
            "prompt": render_prompt(task, repo_name),
            "params": task["repos"][repo_name],
            "scoring": task["scoring"],
        }
        print(json.dumps(output))
    else:
        print(json.dumps(task))
