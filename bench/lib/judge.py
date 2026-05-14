#!/usr/bin/env python3
"""LLM-as-judge for bench scenario answers.

Usage: judge.py <scored.json> <transcript.json> <rubric.yaml> [--out <path>]

Reads the scored result (for side-context: wall_time, tokens, failed),
the transcript (for the assistant answer text), and the per-scenario
rubric. Calls Claude Opus 4.7 once per step with the rubric's four
criteria. Writes judged.json next to scored.json (or to --out).

Reproducibility tuple: {prompt version, model id, scenario rubric}.
temperature is omitted from requests — it's deprecated on
claude-opus-4-7 — so the model runs in its default sampling mode and
the variance baseline (results/judge-variance.md) characterises the
residual non-determinism. The prompt is loaded from judge_prompt.v1.md;
bump the filename when changing it.
"""

import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

import yaml

LIB_DIR = os.path.dirname(os.path.abspath(__file__))
JUDGE_PROMPT_PATH = os.path.join(LIB_DIR, "judge_prompt.v1.md")
JUDGE_PROMPT_VERSION = "v1"
JUDGE_MODEL = "claude-opus-4-7"
ANTHROPIC_API = "https://api.anthropic.com/v1/messages"
ANTHROPIC_VERSION = "2023-06-01"

# Distinct exit code for "API credit/key/quota exhausted". Callers
# (loop orchestrator + audit shell wrappers) treat 42 as "skip the
# remaining API-gated phases this iteration, do not crash the loop."
# See pitch 20-07 §"Credentials & credit fallback".
CREDIT_EXHAUSTED_EXIT_CODE = 42


def _credit_exhausted_signature(http_code: int, body_lower: str) -> str | None:
    """Return a short reason string if this HTTP error means we are out
    of credit / out of key validity / out of quota — otherwise None.
    """
    if http_code == 400 and "credit balance is too low" in body_lower:
        return "400 credit balance is too low"
    if http_code == 401 and (
        "invalid_api_key" in body_lower or "authentication_error" in body_lower
    ):
        return "401 invalid_api_key"
    if http_code == 429 and ("quota" in body_lower or "rate_limit" in body_lower and "balance" in body_lower):
        # Generic 429 retries elsewhere; this branch fires only when the
        # body explicitly cites quota/balance, not transient rate-limit.
        return "429 quota exhausted"
    return None


def _notify_credit_exhausted(caller: str, reason: str) -> None:
    """Loud stderr banner + best-effort macOS notification. The stamp
    file is written by the shell wrapper (improve-loop.sh) so it can
    survive across Python processes and persist across iterations.
    """
    banner = (
        "\n"
        "============================================================\n"
        f"  CREDIT EXHAUSTED — bench/{caller}\n"
        f"  reason: {reason}\n"
        "  loop will skip API-gated phases for the rest of this iteration\n"
        "============================================================\n"
    )
    print(banner, file=sys.stderr, flush=True)
    try:
        subprocess.run(
            [
                "osascript",
                "-e",
                f'display notification "{reason}" with title "bench credit exhausted ({caller})"',
            ],
            timeout=5,
            check=False,
            capture_output=True,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        # No osascript (non-mac), or it failed — banner + exit code remain.
        pass

DEFAULT_WEIGHTS = {
    "map_quality": 0.40,
    "specificity": 0.25,
    "justification": 0.20,
    "uncertainty": 0.15,
}


# ── Answer extraction ────────────────────────────────────────────────


def read_answer_text(transcript_path):
    """Concatenate assistant text blocks from the stream-json transcript."""
    parts = []
    with open(transcript_path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue
            event = obj.get("event", obj)
            if event.get("type") != "assistant":
                continue
            for block in event.get("message", {}).get("content", []):
                if block.get("type") == "text":
                    text = block.get("text", "")
                    if text:
                        parts.append(text)
    return "\n".join(parts)


def slice_answer_for_step(full_answer, step_idx, step_name):
    """Return the segment of the answer for a given step.

    Scenarios run all steps in one Claude session, so the transcript holds
    a single long answer covering every step. Most tools structure their
    synthesis under `## Step N:` headers; we slice between header N and
    header N+1. If headers are missing, we hand back the whole answer —
    the judge can still score, but more loosely.

    step_idx is 0-based; headers use 1-based numbering.
    """
    n = step_idx + 1
    pattern = re.compile(rf'^#{{1,4}}\s*Step\s*{n}\b[^\n]*\n', re.M | re.I)
    matches = list(pattern.finditer(full_answer))
    if not matches:
        return full_answer

    # Some tools repeat "## Step N:" once during work and again in the
    # synthesis. Take the last match as the synthesis section start —
    # that's the authoritative answer for the step.
    m = matches[-1]
    start = m.start()
    next_n = n + 1
    next_pattern = re.compile(rf'^#{{1,4}}\s*Step\s*{next_n}\b[^\n]*\n', re.M | re.I)
    next_matches = list(next_pattern.finditer(full_answer, m.end()))
    end = next_matches[-1].start() if next_matches else len(full_answer)
    return full_answer[start:end]


# ── Rubric loading ──────────────────────────────────────────────────


def load_rubric(rubric_path, scenario_steps):
    """Load and validate a scenario rubric.

    The rubric's step names must match the scenario's step names
    verbatim — order matters, and a mismatch means the judge would
    score the wrong criteria against the wrong step. Hard error.
    """
    if not os.path.exists(rubric_path):
        raise SystemExit(
            f"judge: missing rubric file {rubric_path}. Author a rubric or "
            f"add scenario coverage — judge does not silently default."
        )

    with open(rubric_path) as f:
        rubric = yaml.safe_load(f)

    if not isinstance(rubric, dict) or "audience" not in rubric or "steps" not in rubric:
        raise SystemExit(f"judge: rubric {rubric_path} missing 'audience' or 'steps'")

    rubric_steps = rubric["steps"]
    if len(rubric_steps) != len(scenario_steps):
        raise SystemExit(
            f"judge: rubric has {len(rubric_steps)} steps, scenario has "
            f"{len(scenario_steps)}. Update the rubric."
        )

    for i, (r_step, s_step) in enumerate(zip(rubric_steps, scenario_steps)):
        if r_step.get("name") != s_step.get("name"):
            raise SystemExit(
                f"judge: rubric step {i} name {r_step.get('name')!r} does "
                f"not match scenario step {s_step.get('name')!r}"
            )
        criteria = r_step.get("criteria", {})
        for key in DEFAULT_WEIGHTS:
            if key not in criteria:
                raise SystemExit(
                    f"judge: rubric step {i} missing criterion {key!r}"
                )

    return rubric


def format_rubric_for_prompt(rubric):
    """Render the rubric as a block suitable for the system prompt."""
    lines = ["# Audience", "", rubric["audience"].strip(), ""]
    lines.append("# Rubric")
    lines.append("")
    for i, step in enumerate(rubric["steps"], 1):
        lines.append(f"## Step {i}: {step['name']}")
        for key in ("map_quality", "specificity", "justification", "uncertainty"):
            crit = step["criteria"][key]
            weight = crit.get("weight", DEFAULT_WEIGHTS[key])
            question = crit["question"].strip()
            lines.append(f"- **{key}** (weight {weight}): {question}")
        lines.append("")
    return "\n".join(lines)


# ── Anthropic API call ───────────────────────────────────────────────


def call_judge(system_text, user_text, *, api_key, max_tokens=1024, retries=3):
    """POST to Anthropic Messages API, return parsed JSON content.

    Caches the system block (audience + full scenario rubric + judge
    instructions) so 12 sessions × 4 steps × 1 scenario share a single
    cached prefix. Cache miss only on the first call per scenario.
    """
    payload = {
        "model": JUDGE_MODEL,
        "max_tokens": max_tokens,
        # No temperature: deprecated on claude-opus-4-7. The model runs
        # in its default deterministic-ish sampling mode, and the variance
        # baseline (results/judge-variance.md) measures whatever residual
        # non-determinism remains.
        "system": [
            {
                "type": "text",
                "text": system_text,
                "cache_control": {"type": "ephemeral"},
            }
        ],
        "messages": [{"role": "user", "content": user_text}],
    }

    headers = {
        "x-api-key": api_key,
        "anthropic-version": ANTHROPIC_VERSION,
        "content-type": "application/json",
    }

    data = json.dumps(payload).encode("utf-8")
    last_err = None
    for attempt in range(retries):
        req = urllib.request.Request(
            ANTHROPIC_API, data=data, headers=headers, method="POST"
        )
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                body = resp.read().decode("utf-8")
                return json.loads(body)
        except urllib.error.HTTPError as e:
            err_body = e.read().decode("utf-8", errors="replace")
            last_err = f"HTTP {e.code}: {err_body[:500]}"
            # Credit / key / quota exhaustion — distinct path. The loop
            # must keep running on subscription for scenario sessions,
            # so we exit 42 (not 1) and let the orchestrator decide.
            reason = _credit_exhausted_signature(e.code, err_body.lower())
            if reason is not None:
                caller = os.environ.get("BENCH_JUDGE_CALLER", "judge.py")
                _notify_credit_exhausted(caller, reason)
                sys.exit(CREDIT_EXHAUSTED_EXIT_CODE)
            # 429/5xx are worth retrying with backoff; other 4xx aren't.
            if e.code in (429, 500, 502, 503, 504) and attempt < retries - 1:
                time.sleep(2 ** attempt)
                continue
            raise SystemExit(f"judge: API call failed: {last_err}")
        except urllib.error.URLError as e:
            last_err = f"network: {e}"
            if attempt < retries - 1:
                time.sleep(2 ** attempt)
                continue
            raise SystemExit(f"judge: API call failed: {last_err}")

    raise SystemExit(f"judge: API call failed after {retries} attempts: {last_err}")


def extract_usage(api_response):
    """Return the Anthropic Messages API usage dict (or zeros). All cost
    accounting downstream is computed from these counts × public per-token
    pricing — see lib/scorer.PRICE_PER_M. A token is a token regardless
    of whether the call was actually billed via API key or subscription;
    cost in this bench is a comparability metric, not an accounting one.
    """
    u = api_response.get("usage") or {}
    return {
        "input_tokens": u.get("input_tokens", 0) or 0,
        "output_tokens": u.get("output_tokens", 0) or 0,
        "cache_creation_input_tokens": u.get("cache_creation_input_tokens", 0) or 0,
        "cache_read_input_tokens": u.get("cache_read_input_tokens", 0) or 0,
    }


def extract_judge_json(api_response):
    """Pull the assistant's JSON object out of an Anthropic Messages response.

    Tolerates the judge wrapping JSON in markdown fences even though the
    prompt asks not to — strips them if present. Returns the parsed object,
    or raises a clear error pointing at what came back.
    """
    content = api_response.get("content", [])
    text = ""
    for block in content:
        if block.get("type") == "text":
            text += block.get("text", "")
    text = text.strip()

    if text.startswith("```"):
        text = re.sub(r"^```(?:json)?\s*", "", text)
        text = re.sub(r"\s*```$", "", text)

    try:
        return json.loads(text)
    except json.JSONDecodeError as e:
        raise SystemExit(
            f"judge: model returned non-JSON content: {e}\nGot: {text[:500]!r}"
        )


# ── Per-step scoring ─────────────────────────────────────────────────


def compute_step_quality(scores):
    """Weighted sum of criterion scores using DEFAULT_WEIGHTS."""
    total = 0.0
    for key, weight in DEFAULT_WEIGHTS.items():
        crit = scores.get(key, {})
        score = float(crit.get("score", 0.0))
        score = max(0.0, min(1.0, score))
        total += weight * score
    return round(total, 4)


def judge_step(*, step_idx, step, answer_slice, system_text, side_context,
               api_key):
    """Run the judge on one step. Returns the step's quality record."""
    user_blocks = [
        f"Score step: {step['name']!r}",
        "",
        "Step prompt:",
        step["prompt"].strip(),
        "",
        "Answer to score:",
        answer_slice if answer_slice else "(empty — the tool produced no answer for this step)",
        "",
        "Side-context:",
        json.dumps(side_context),
    ]
    user_text = "\n".join(user_blocks)

    response = call_judge(system_text, user_text, api_key=api_key)
    parsed = extract_judge_json(response)

    raw_scores = parsed.get("scores", {})
    normalised = {}
    for key in DEFAULT_WEIGHTS:
        crit = raw_scores.get(key, {})
        normalised[key] = {
            "score": max(0.0, min(1.0, float(crit.get("score", 0.0)))),
            "rationale": str(crit.get("rationale", "")).strip(),
        }

    step_quality = compute_step_quality(normalised)

    usage = response.get("usage", {})

    return {
        "step": step["name"],
        "scores": normalised,
        "step_quality": step_quality,
        "usage": {
            "input_tokens": usage.get("input_tokens", 0),
            "output_tokens": usage.get("output_tokens", 0),
            "cache_creation_input_tokens": usage.get("cache_creation_input_tokens", 0),
            "cache_read_input_tokens": usage.get("cache_read_input_tokens", 0),
        },
    }


# ── CLI ──────────────────────────────────────────────────────────────


def find_scenario_for_rubric(rubric_path):
    """The scenario yaml sits next to the rubric: foo.yaml ↔ foo.rubric.yaml."""
    base = rubric_path.replace(".rubric.yaml", ".yaml")
    if not os.path.exists(base):
        raise SystemExit(f"judge: scenario file not found at {base}")
    return base


def main(argv):
    if len(argv) < 4:
        print(
            "Usage: judge.py <scored.json> <transcript.json> <rubric.yaml> "
            "[--out <judged.json>]",
            file=sys.stderr,
        )
        sys.exit(1)

    scored_path = argv[1]
    transcript_path = argv[2]
    rubric_path = argv[3]
    out_path = None
    if "--out" in argv:
        out_path = argv[argv.index("--out") + 1]
    if out_path is None:
        out_path = os.path.join(os.path.dirname(scored_path), "judged.json")

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        raise SystemExit("judge: ANTHROPIC_API_KEY not set")

    with open(scored_path) as f:
        scored = json.load(f)

    # Failed runs short-circuit — fairness=0 already, the judge has no answer
    # to score, and we should not bill an Opus call for an empty transcript.
    if scored.get("failed"):
        judged = {
            "scenario": scored.get("scenario"),
            "repo": scored.get("repo"),
            "judge": {
                "model": JUDGE_MODEL,
                "prompt_version": JUDGE_PROMPT_VERSION,
                "rubric_path": os.path.basename(rubric_path),
                "skipped_reason": "run_failed",
            },
            "scenario_quality": 0.0,
            "steps": [],
        }
        with open(out_path, "w") as f:
            json.dump(judged, f, indent=2)
            f.write("\n")
        print(f"Judged (skipped, failed run): → {out_path}", file=sys.stderr)
        return

    # Load scenario via the existing parser so defaults/validation match scorer.
    sys.path.insert(0, LIB_DIR)
    from scenario import parse as parse_scenario

    scenario_path = find_scenario_for_rubric(rubric_path)
    scenario = parse_scenario(scenario_path)
    rubric = load_rubric(rubric_path, scenario["steps"])

    with open(JUDGE_PROMPT_PATH) as f:
        judge_prompt = f.read()

    system_text = judge_prompt + "\n\n" + format_rubric_for_prompt(rubric)

    full_answer = read_answer_text(transcript_path)

    metrics = scored.get("metrics", {})
    base_side_context = {
        "wall_time_seconds": metrics.get("wall_time_seconds", 0),
        "total_tokens": metrics.get("token_total_billed", 0),
        "completed": not scored.get("failed", False),
    }

    step_results = []
    for i, step in enumerate(scenario["steps"]):
        answer_slice = slice_answer_for_step(full_answer, i, step["name"])
        result = judge_step(
            step_idx=i,
            step=step,
            answer_slice=answer_slice,
            system_text=system_text,
            side_context=base_side_context,
            api_key=api_key,
        )
        step_results.append(result)
        print(
            f"  step {i+1}/{len(scenario['steps'])}: {step['name'][:50]} "
            f"→ quality={result['step_quality']}",
            file=sys.stderr,
        )

    scenario_quality = (
        round(sum(s["step_quality"] for s in step_results) / len(step_results), 4)
        if step_results else 0.0
    )

    judged = {
        "scenario": scenario["name"],
        "repo": scenario["repo"],
        "judge": {
            "model": JUDGE_MODEL,
            "prompt_version": JUDGE_PROMPT_VERSION,
            "rubric_path": os.path.basename(rubric_path),
        },
        "scenario_quality": scenario_quality,
        "steps": step_results,
    }

    with open(out_path, "w") as f:
        json.dump(judged, f, indent=2)
        f.write("\n")

    print(
        f"Judged: {scenario['name']} → {out_path} "
        f"(scenario_quality={scenario_quality})",
        file=sys.stderr,
    )


if __name__ == "__main__":
    main(sys.argv)
