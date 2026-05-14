#!/usr/bin/env python3
"""Scenario auditor — what does the LLM understand that the checks don't reward?

Usage: audit_scenarios.py <scenario.yaml> <results_root> [--out <path>]

Reads both sense and baseline scored/transcript/judged for one scenario
and produces audit-scenarios.json: per-check non-discrimination signal
(both tools always hit or always miss) computed deterministically, plus
judge-proposed missing-signal checks (one judge call per scenario, both
transcripts side-by-side).

The output is shaped so the next iteration's Phase 2 reviewer can treat
proposals as *hints* — it inherits the existing rollback safety net.
"""

import json
import os
import sys

LIB_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, LIB_DIR)

from judge import (  # noqa: E402
    JUDGE_MODEL,
    call_judge,
    extract_judge_json,
    read_answer_text,
)
from scenario import parse as parse_scenario  # noqa: E402

PROMPT_VERSION = "v1"
TOOLS = ("sense", "baseline")
MAX_MISSING_SIGNALS = 8

SYSTEM_PROMPT = """You are auditing the *checks* of a code-intelligence
benchmark. The benchmark scores tools by hit-rate against keyword-style
checks (`word`, `phrase`, `contains`, `response_richness`). Each scenario
has 3-4 steps; each step has 3-8 checks.

You are looking for **missing signals**: facts that one tool's answer
demonstrates and the other's doesn't, where the difference is real
understanding (concrete file:line, named identifier, edge case, test
breakage warning) but no current check rewards it. These are candidate
checks to add.

You will receive the scenario, all current checks per step, and both
tools' answers side-by-side. Do NOT propose checks that:

- already exist (review the current-checks list)
- reward keyword stuffing without demonstrating understanding (e.g. a
  bare class name that both tools mention as a header)
- depend on the tool's chain-of-thought; the check runs on the final
  answer text only

A good proposal cites the evidence: which tool demonstrated this in the
transcript, what the differentiating fragment is. Prefer `phrase` or
`contains` checks with values that capture the *specific* understanding
(`spec/integration/spam_rules_spec`, `EnsureMagic raises InvalidAccess`)
over loose word checks.

Return a JSON object exactly of this shape, no markdown fences, no prose
before or after. Cap proposals at 8; quality over quantity:

{
  "missing_signals": [
    {
      "step_idx": 0,
      "observed": "one short sentence describing the differentiating evidence",
      "demonstrated_by": "sense" | "baseline" | "both",
      "suggestion_check": {
        "type": "word" | "phrase" | "contains",
        "value": "<the literal string to match in the answer>",
        "required": false,
        "description": "<one short sentence on what this rewards>"
      }
    }
  ]
}

If you find nothing genuinely missing, return {"missing_signals": []}. Do
not invent signals to fill the quota.
"""


# ── Deterministic: non-discriminating checks ───────────────────────────


def non_discriminating(per_tool_scored, scenario):
    """Find fairness checks where every tool hit or every tool missed.

    With a single run per tool, "rate" is 0 or 1. We surface these as
    candidates the reviewer can prune (kept-on-everyone or kept-off-everyone
    checks add no information).
    """
    findings = []
    n_steps = len(scenario["steps"])
    for step_idx in range(n_steps):
        # All tools must have a scored step at this index; otherwise skip
        # (failed runs are excluded elsewhere).
        tool_steps = {
            tool: scored["steps"][step_idx]
            for tool, scored in per_tool_scored.items()
            if step_idx < len(scored.get("steps", []))
        }
        if len(tool_steps) < len(TOOLS):
            continue

        check_count = len(tool_steps[TOOLS[0]].get("checks", []))
        for check_idx in range(check_count):
            checks_by_tool = {}
            for tool, step in tool_steps.items():
                checks = step.get("checks", [])
                if check_idx >= len(checks):
                    checks_by_tool = {}
                    break
                checks_by_tool[tool] = checks[check_idx]
            if not checks_by_tool:
                continue

            ref = next(iter(checks_by_tool.values()))
            if ref.get("layer") and ref["layer"] != "fairness":
                continue

            rates = {
                tool: 1.0 if c.get("hit") else 0.0
                for tool, c in checks_by_tool.items()
            }
            all_hit = all(r == 1.0 for r in rates.values())
            all_miss = all(r == 0.0 for r in rates.values())
            if not (all_hit or all_miss):
                continue

            suggestion = (
                "remove — both tools always hit this trivially"
                if all_hit
                else "remove or tighten — both tools always miss this; not discriminating either"
            )
            findings.append({
                "step_idx": step_idx,
                "check_idx": check_idx,
                "check_type": ref["type"],
                "check_value": ref["value"],
                **{f"{tool}_rate": rates[tool] for tool in TOOLS},
                "suggestion": suggestion,
            })
    return findings


# ── Judge: missing signals ────────────────────────────────────────────


def build_judge_payload(scenario, per_tool_answer):
    """Build the user-message payload for the judge call."""
    blocks = [
        f"## Scenario: {scenario['name']}",
        f"Repository: {scenario['repo']}",
        "",
        scenario["description"].strip(),
        "",
    ]
    for i, step in enumerate(scenario["steps"]):
        blocks.append(f"### Step {i}: {step['name']}")
        blocks.append("")
        blocks.append("Prompt:")
        blocks.append(step["prompt"].strip())
        blocks.append("")
        blocks.append("Current checks (fairness layer only):")
        fairness = [
            c for c in step.get("checks", [])
            if (c.get("layer") or "fairness") == "fairness"
        ]
        for c in fairness:
            blocks.append(
                f"  - type={c['type']} value={c['value']!r} required={c.get('required', True)}"
            )
        blocks.append("")
        for tool, answer in per_tool_answer.items():
            blocks.append(f"#### {tool} answer for step {i}")
            blocks.append(answer.get(i, "(no answer for this step)").strip())
            blocks.append("")
    return "\n".join(blocks)


def call_missing_signals(scenario, per_tool_answer, api_key):
    user_text = build_judge_payload(scenario, per_tool_answer)
    response = call_judge(
        SYSTEM_PROMPT, user_text, api_key=api_key, max_tokens=2048
    )
    parsed = extract_judge_json(response)
    signals = parsed.get("missing_signals", [])
    out = []
    for s in signals[:MAX_MISSING_SIGNALS]:
        sugg = s.get("suggestion_check", {}) or {}
        if not sugg.get("type") or not sugg.get("value"):
            continue
        out.append({
            "step_idx": int(s.get("step_idx", 0)),
            "observed": str(s.get("observed", "")).strip(),
            "demonstrated_by": str(s.get("demonstrated_by", "")).strip(),
            "suggestion_check": {
                "type": str(sugg["type"]),
                "value": str(sugg["value"]),
                "required": bool(sugg.get("required", False)),
                "description": str(sugg.get("description", "")).strip(),
            },
        })
    usage = response.get("usage", {})
    return out, usage


# ── Per-step answer slicing ────────────────────────────────────────────


def slice_all_steps(full_answer, scenario):
    """Return {step_idx: answer_slice} for every scenario step."""
    from judge import slice_answer_for_step
    return {
        i: slice_answer_for_step(full_answer, i, step["name"])
        for i, step in enumerate(scenario["steps"])
    }


# ── CLI ────────────────────────────────────────────────────────────────


def main(argv):
    if len(argv) < 3:
        print(
            "Usage: audit_scenarios.py <scenario.yaml> <results_root> "
            "[--out <audit-scenarios.json>]",
            file=sys.stderr,
        )
        sys.exit(1)

    scenario_path = argv[1]
    results_root = argv[2]
    out_path = None
    if "--out" in argv:
        out_path = argv[argv.index("--out") + 1]

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        raise SystemExit("audit_scenarios: ANTHROPIC_API_KEY not set")

    scenario = parse_scenario(scenario_path)
    repo = scenario["repo"]

    if out_path is None:
        out_path = os.path.join(results_root, f"audit-scenarios.{repo}.json")

    per_tool_scored = {}
    per_tool_answer = {}
    skipped_tools = []
    for tool in TOOLS:
        scored_path = os.path.join(results_root, tool, repo, "scored.json")
        transcript_path = os.path.join(results_root, tool, repo, "transcript.json")
        if not os.path.exists(scored_path) or not os.path.exists(transcript_path):
            skipped_tools.append(tool)
            continue
        with open(scored_path) as f:
            scored = json.load(f)
        if scored.get("failed"):
            skipped_tools.append(tool)
            continue
        per_tool_scored[tool] = scored
        full_answer = read_answer_text(transcript_path)
        per_tool_answer[tool] = slice_all_steps(full_answer, scenario)

    if len(per_tool_scored) < len(TOOLS):
        payload = {
            "scenario": scenario["name"],
            "repo": repo,
            "audit": {"model": JUDGE_MODEL, "prompt_version": PROMPT_VERSION},
            "non_discriminating_checks": [],
            "missing_signals": [],
            "skipped_reason": f"tools missing or failed: {skipped_tools}",
        }
        with open(out_path, "w") as f:
            json.dump(payload, f, indent=2)
            f.write("\n")
        print(
            f"audit_scenarios (skipped: {skipped_tools}): → {out_path}",
            file=sys.stderr,
        )
        return

    non_disc = non_discriminating(per_tool_scored, scenario)
    print(
        f"  {repo}: {len(non_disc)} non-discriminating checks "
        f"(deterministic from scored.json)",
        file=sys.stderr,
    )

    missing, usage = call_missing_signals(scenario, per_tool_answer, api_key)
    print(
        f"  {repo}: {len(missing)} missing-signal proposals "
        f"(judge call, in/out tokens={usage.get('input_tokens', 0)}/"
        f"{usage.get('output_tokens', 0)})",
        file=sys.stderr,
    )

    payload = {
        "scenario": scenario["name"],
        "repo": repo,
        "audit": {
            "model": JUDGE_MODEL,
            "prompt_version": PROMPT_VERSION,
        },
        "non_discriminating_checks": non_disc,
        "missing_signals": missing,
        "usage": {
            "input_tokens": usage.get("input_tokens", 0),
            "output_tokens": usage.get("output_tokens", 0),
            "cache_creation_input_tokens": usage.get("cache_creation_input_tokens", 0),
            "cache_read_input_tokens": usage.get("cache_read_input_tokens", 0),
        },
    }

    with open(out_path, "w") as f:
        json.dump(payload, f, indent=2)
        f.write("\n")

    print(
        f"Audit (scenarios): {repo} → {out_path} "
        f"(non_discriminating={len(non_disc)}, missing_signals={len(missing)})",
        file=sys.stderr,
    )


if __name__ == "__main__":
    main(sys.argv)
