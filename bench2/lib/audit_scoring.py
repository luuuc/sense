#!/usr/bin/env python3
"""Score auditor — does the mechanical scorer agree with a reading judge?

Usage: audit_scoring.py <scored.json> <transcript.json> <scenario.yaml> [--out <path>]

For each fairness-layer check the scorer recorded a verdict for, ask the
judge whether that verdict (hit/miss) matches what the answer text actually
demonstrates. **One judge call per (tool, repo)** — all fairness checks
for one transcript are batched into a single prompt so 12 transcripts
cost 12 calls per iteration, not hundreds.

The judge emits one of `agree | disagree | unsure` per check. Output
(audit-scoring.json) carries a summary plus the top-N disagreements by
judge confidence; the full per-check results live alongside in
audit-scoring-full.json. Adoption-layer checks (mcp_tool_used, no_grep)
are skipped — they score tool behaviour, not the answer.
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
    extract_usage,
    read_answer_text,
    slice_answer_for_step,
)
from scenario import parse as parse_scenario  # noqa: E402

PROMPT_VERSION = "v1"
DISAGREEMENT_RATE_THRESHOLD = 0.05
TOP_N_DISAGREEMENTS = 20

SYSTEM_PROMPT = """You are auditing a mechanical scorer for a code-intelligence
benchmark. The scorer ran simple rules (substring, word, phrase, file-richness
threshold) over the FULL answer text and recorded a hit/miss verdict for each
check. The scorer's scope is the entire multi-step answer — a `word` check
hits if its value appears anywhere in the full answer, regardless of which
step's section it lives in. Mirror this scope when auditing: do not penalise
a hit just because the keyword appears in a different step's section.

You will receive one scenario step at a time, with:

- the step prompt (what the tool was asked)
- the focused section of the answer for that step (for context only)
- the full multi-step answer (the scope the scorer used)
- a list of fairness-layer checks: id, type, value, engine_verdict

For each check, decide one of:

- "agree"    — the engine_verdict matches the answer at the scorer's scope
               (the full answer)
- "disagree" — the engine_verdict is wrong; either the answer clearly
               demonstrates the intended understanding and the scorer
               marked miss, or the match landed on the wrong meaning of
               the value (e.g. `ensure` matched the identifier
               `EnsureMagic` rather than the Ruby keyword the scenario
               asked about) and the scorer marked hit
- "unsure"   — the answer is genuinely ambiguous, or the check intent
               cannot be inferred from `value` alone

For `response_richness` checks, trust the engine's count unless the
answer is obviously inconsistent with it — these are mechanical.

Score `confidence` honestly: 1.0 means the answer text settles the
question; 0.3 means you are guessing. Top disagreements are surfaced to
a human by confidence, so calibrate.

Return a single JSON object exactly of this shape, no markdown fences,
no prose before or after:

{
  "verdicts": [
    {
      "check_id": "<echoed back>",
      "verdict": "agree" | "disagree" | "unsure",
      "confidence": 0.0-1.0,
      "rationale": "one short sentence; quote the offending fragment when disagreeing"
    }
  ]
}

The list must contain exactly one entry per check you were given, with
check_id matching what was provided. Do not add extras or drop entries.
"""


# ── Check selection ────────────────────────────────────────────────────


def fairness_checks_for_step(step, step_idx):
    """Yield (check_id, check) for fairness-layer checks in a scored step."""
    for j, check in enumerate(step.get("checks", [])):
        if check.get("layer") and check["layer"] != "fairness":
            continue
        yield f"{step_idx}.{j}", check


# ── Per-step batch audit ───────────────────────────────────────────────


def audit_step(*, step_idx, scenario_step, scored_step, answer_slice,
               full_answer, api_key):
    """Ask the judge to verdict every fairness check in this step at once.

    Returns the parsed verdicts list (one entry per check). One judge call
    per step keeps tokens bounded per call (long transcripts × 30 checks
    in one prompt is fragile) while still hitting the "one call per
    transcript" cost budget after step batching across all 3-4 steps —
    effectively 3-4 calls per (tool, repo) transcript, not per-check.

    Both the step slice and the full answer are passed: the slice gives
    the judge focused context, the full answer matches the scope the
    scorer used (a `word` check hits anywhere in the full multi-step
    answer, so the auditor must too).
    """
    checks = list(fairness_checks_for_step(scored_step, step_idx))
    if not checks:
        return []

    check_blocks = []
    for check_id, check in checks:
        engine_verdict = "hit" if check.get("hit") else "miss"
        check_blocks.append(
            {
                "check_id": check_id,
                "type": check["type"],
                "value": check["value"],
                "engine_verdict": engine_verdict,
            }
        )

    user_text = json.dumps(
        {
            "step_name": scenario_step["name"],
            "step_prompt": scenario_step["prompt"].strip(),
            "step_section": answer_slice if answer_slice else "(empty)",
            "full_answer": full_answer if full_answer else "(empty)",
            "checks_to_audit": check_blocks,
        },
        indent=2,
    )

    response = call_judge(
        SYSTEM_PROMPT, user_text, api_key=api_key,
        max_tokens=min(4096, 200 + 120 * len(check_blocks)),
    )
    parsed = extract_judge_json(response)
    usage = extract_usage(response)
    raw_verdicts = parsed.get("verdicts", [])

    by_id = {str(v.get("check_id")): v for v in raw_verdicts}
    results = []
    for check_id, check in checks:
        v = by_id.get(check_id, {})
        verdict = v.get("verdict", "unsure")
        if verdict not in {"agree", "disagree", "unsure"}:
            verdict = "unsure"
        confidence = float(v.get("confidence", 0.0))
        confidence = max(0.0, min(1.0, confidence))
        results.append(
            {
                "check_id": check_id,
                "step": scenario_step["name"],
                "check": {"type": check["type"], "value": check["value"]},
                "engine_verdict": "hit" if check.get("hit") else "miss",
                "judge_verdict": verdict,
                "confidence": confidence,
                "rationale": str(v.get("rationale", "")).strip(),
            }
        )
    return results, usage


# ── CLI ────────────────────────────────────────────────────────────────


def main(argv):
    if len(argv) < 4:
        print(
            "Usage: audit_scoring.py <scored.json> <transcript.json> "
            "<scenario.yaml> [--out <audit-scoring.json>]",
            file=sys.stderr,
        )
        sys.exit(1)

    scored_path = argv[1]
    transcript_path = argv[2]
    scenario_path = argv[3]

    out_path = None
    if "--out" in argv:
        out_path = argv[argv.index("--out") + 1]
    if out_path is None:
        out_path = os.path.join(os.path.dirname(scored_path), "audit-scoring.json")
    full_path = out_path.replace(".json", "-full.json")

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        raise SystemExit("audit_scoring: ANTHROPIC_API_KEY not set")

    with open(scored_path) as f:
        scored = json.load(f)

    repo = scored.get("repo", "?")
    tool = os.path.basename(os.path.dirname(os.path.dirname(scored_path)))

    if scored.get("failed"):
        payload = {
            "tool": tool,
            "repo": repo,
            "audit": {"model": JUDGE_MODEL, "prompt_version": PROMPT_VERSION},
            "total_checks": 0,
            "agreed": 0,
            "disagreed": 0,
            "unsure": 0,
            "disagreement_rate": 0.0,
            "disagreements": [],
            "skipped_reason": "run_failed",
        }
        with open(out_path, "w") as f:
            json.dump(payload, f, indent=2)
            f.write("\n")
        print(f"audit_scoring (skipped, failed run): → {out_path}", file=sys.stderr)
        return

    scenario = parse_scenario(scenario_path)
    full_answer = read_answer_text(transcript_path)

    all_results = []
    usage_total = {
        "input_tokens": 0,
        "output_tokens": 0,
        "cache_creation_input_tokens": 0,
        "cache_read_input_tokens": 0,
    }
    for step_idx, scored_step in enumerate(scored.get("steps", [])):
        scenario_step = scenario["steps"][step_idx]
        answer_slice = slice_answer_for_step(
            full_answer, step_idx, scenario_step["name"]
        )
        step_results, step_usage = audit_step(
            step_idx=step_idx,
            scenario_step=scenario_step,
            scored_step=scored_step,
            answer_slice=answer_slice,
            full_answer=full_answer,
            api_key=api_key,
        )
        for r in step_results:
            r["repo"] = repo
            r["tool"] = tool
        all_results.extend(step_results)
        for k in usage_total:
            usage_total[k] += step_usage.get(k, 0)
        d = sum(1 for r in step_results if r["judge_verdict"] == "disagree")
        u = sum(1 for r in step_results if r["judge_verdict"] == "unsure")
        a = sum(1 for r in step_results if r["judge_verdict"] == "agree")
        print(
            f"  {tool}/{repo} step {step_idx+1}: {a} agree, {d} disagree, {u} unsure",
            file=sys.stderr,
        )

    total = len(all_results)
    agreed = sum(1 for r in all_results if r["judge_verdict"] == "agree")
    disagreed = sum(1 for r in all_results if r["judge_verdict"] == "disagree")
    unsure = sum(1 for r in all_results if r["judge_verdict"] == "unsure")
    rate = round((disagreed + unsure) / total, 4) if total else 0.0

    disagreements = [r for r in all_results if r["judge_verdict"] != "agree"]
    disagreements.sort(key=lambda r: r["confidence"], reverse=True)

    summary = {
        "tool": tool,
        "repo": repo,
        "audit": {
            "model": JUDGE_MODEL,
            "prompt_version": PROMPT_VERSION,
        },
        "total_checks": total,
        "agreed": agreed,
        "disagreed": disagreed,
        "unsure": unsure,
        "disagreement_rate": rate,
        "rate_threshold": DISAGREEMENT_RATE_THRESHOLD,
        "over_threshold": rate > DISAGREEMENT_RATE_THRESHOLD,
        "disagreements": disagreements[:TOP_N_DISAGREEMENTS],
        "usage": usage_total,
    }

    with open(out_path, "w") as f:
        json.dump(summary, f, indent=2)
        f.write("\n")

    with open(full_path, "w") as f:
        json.dump(
            {
                "tool": tool,
                "repo": repo,
                "audit": summary["audit"],
                "results": all_results,
            },
            f,
            indent=2,
        )
        f.write("\n")

    print(
        f"Audit (scoring): {tool}/{repo} → {out_path} "
        f"(disagreement_rate={rate:.3f}, {disagreed}d/{unsure}u/{agreed}a of {total})",
        file=sys.stderr,
    )


if __name__ == "__main__":
    main(sys.argv)
