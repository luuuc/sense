"""Re-score the frozen held-out transcripts against the current rubric.

This is the missing input for convergence criterion 4 (held-out validation
correlation). Each iteration of the improvement loop calls this script to
re-judge the frozen held-out transcripts under the current scoring config
and emit `iter-N/validation/held-out-scored.json`. The convergence
evaluator then compares those llm_quality numbers against the hand-graded
`*.gold.json` files via Spearman correlation.

The transcripts themselves are never re-run — that's the whole point of
"held-out". Only re-scoring with the current rubric. Locked.yaml's
held_out_lockfile gates this: if any held-out file has drifted, the
caller refuses to run.

Per-iteration cost: ~6 × judge call × $0.30 = ~$1.50.

Usage:
    python3 bench/lib/heldout_rescore.py --iter-dir PATH \\
        [--held-out-dir PATH] [--force]
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from typing import Any

LIB_DIR = os.path.dirname(os.path.abspath(__file__))
BENCH_DIR = os.path.abspath(os.path.join(LIB_DIR, ".."))
PROJECT_ROOT = os.path.abspath(os.path.join(BENCH_DIR, ".."))
DEFAULT_HELD_OUT_DIR = os.path.join(BENCH_DIR, "scenarios", "held-out")


def _load(path: str) -> Any:
    with open(path) as f:
        return json.load(f)


def discover_pairs(held_out_dir: str) -> list[dict[str, str]]:
    """For every (tool, repo) under transcripts/, find:
      - transcript_path
      - scenario_path  (matched by repo → scenario.repo)
      - rubric_path    (sibling of scenario)
      - gold_path      (sibling of scenario)
    """
    transcripts_root = os.path.join(held_out_dir, "transcripts")
    if not os.path.isdir(transcripts_root):
        return []

    # Build a {repo: (scenario.yaml, rubric.yaml, gold.json)} map.
    import yaml
    by_repo: dict[str, dict[str, str]] = {}
    for entry in sorted(os.listdir(held_out_dir)):
        if not entry.endswith(".yaml") or entry.endswith(".rubric.yaml"):
            continue
        scen_path = os.path.join(held_out_dir, entry)
        with open(scen_path) as f:
            d = yaml.safe_load(f) or {}
        repo = d.get("repo")
        if not repo:
            continue
        slug = entry.removesuffix(".yaml")
        rubric_path = os.path.join(held_out_dir, f"{slug}.rubric.yaml")
        gold_path = os.path.join(held_out_dir, f"{slug}.gold.json")
        by_repo[repo] = {
            "scenario_slug": slug,
            "scenario_path": scen_path,
            "rubric_path": rubric_path,
            "gold_path": gold_path,
        }

    pairs: list[dict[str, str]] = []
    for tool in sorted(os.listdir(transcripts_root)):
        tool_dir = os.path.join(transcripts_root, tool)
        if not os.path.isdir(tool_dir):
            continue
        for repo in sorted(os.listdir(tool_dir)):
            repo_dir = os.path.join(tool_dir, repo)
            transcript = os.path.join(repo_dir, "transcript.json")
            if not os.path.isfile(transcript):
                continue
            meta = by_repo.get(repo)
            if meta is None:
                # No scenario for this repo — skip.
                continue
            pairs.append({
                "tool": tool,
                "repo": repo,
                "transcript_path": transcript,
                **meta,
            })
    return pairs


def rescore_one(
    tool: str,
    repo: str,
    transcript_path: str,
    scenario_path: str,
    rubric_path: str,
    out_dir: str,
    force: bool,
) -> dict[str, Any] | None:
    """Run judge.py once on the frozen transcript, return its scenario_quality.

    Held-out transcripts don't have a paired scored.json (we never run
    score.sh on held-out). judge.py needs both `scored.json` and the
    transcript. Workaround: synthesise a minimal scored.json from the
    transcript and the scenario (judge only reads metrics + step names).
    """
    judge_py = os.path.join(LIB_DIR, "judge.py")
    out_path = os.path.join(out_dir, f"{tool}.{repo}.judged.json")
    if os.path.exists(out_path) and not force:
        with open(out_path) as f:
            return json.load(f)

    # Synthesise a minimal scored.json — judge.py uses it for step
    # iteration. Reusing real scorer would require re-running score.sh
    # on held-out transcripts every iteration, which is fine if we want
    # to be thorough; for now we keep it minimal.
    from scorer import score_transcript  # noqa: WPS433 — lazy import
    from scenario import parse as parse_scenario

    scenario = parse_scenario(scenario_path)
    # Run scoring against the held-out transcript so judge has step data
    # to grade. score_transcript does not require run_meta — but we don't
    # have a result_dir to point at, so pass the transcript dir.
    result_dir = os.path.dirname(transcript_path)
    scored = score_transcript(
        transcript_path, scenario, result_dir, repo_checkout=None
    )
    scored_path = os.path.join(result_dir, "scored.json")
    # Write only if absent — frozen tree should stay clean. Write to a
    # tempfile under out_dir instead so we don't pollute the held-out dir.
    tmp_scored = os.path.join(out_dir, f"{tool}.{repo}.scored.json")
    with open(tmp_scored, "w") as f:
        json.dump(scored, f, indent=2)
        f.write("\n")

    # judge.py expects: judge.py <scored.json> <transcript.json> <rubric.yaml> [--out PATH]
    cmd = [
        "python3", judge_py,
        tmp_scored, transcript_path, rubric_path,
        "--out", out_path,
    ]
    env = os.environ.copy()
    env.setdefault("BENCH_JUDGE_CALLER", "heldout_rescore")
    proc = subprocess.run(cmd, capture_output=True, text=True, env=env)
    if proc.returncode != 0:
        # 42 = credit-exhausted; surface it the same way as judge.py does
        print(f"[heldout_rescore] judge.py failed for {tool}/{repo} (rc={proc.returncode})", file=sys.stderr)
        print(proc.stderr, file=sys.stderr)
        if proc.returncode == 42:
            sys.exit(42)
        return None
    with open(out_path) as f:
        return json.load(f)


def write_validation(iter_dir: str, per_pair: dict[str, dict[str, Any]]) -> str:
    """Write iter-N/validation/held-out-scored.json in the schema
    convergence.py expects: {"<tool>/<repo>": {"llm_quality": float}, ...}
    """
    out_dir = os.path.join(iter_dir, "validation")
    os.makedirs(out_dir, exist_ok=True)
    out_path = os.path.join(out_dir, "held-out-scored.json")
    payload: dict[str, Any] = {}
    for key, judged in per_pair.items():
        if judged is None:
            payload[key] = {"llm_quality": None}
            continue
        payload[key] = {
            "llm_quality": judged.get("scenario_quality"),
            "judge": judged.get("judge"),
        }
    with open(out_path, "w") as f:
        json.dump(payload, f, indent=2)
        f.write("\n")
    return out_path


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--iter-dir", required=True, help="loop-N-iter-M directory")
    p.add_argument("--held-out-dir", default=DEFAULT_HELD_OUT_DIR)
    p.add_argument(
        "--force",
        action="store_true",
        help="re-judge even if held-out-scored.json already exists",
    )
    args = p.parse_args()

    pairs = discover_pairs(args.held_out_dir)
    if not pairs:
        print("[heldout_rescore] no held-out (tool, repo) pairs found", file=sys.stderr)
        return 1

    work_dir = os.path.join(args.iter_dir, "validation", "_judge-cache")
    os.makedirs(work_dir, exist_ok=True)

    per_pair: dict[str, dict[str, Any]] = {}
    for pair in pairs:
        key = f"{pair['tool']}/{pair['repo']}"
        print(f"[heldout_rescore] re-judging {key}...", file=sys.stderr)
        judged = rescore_one(
            tool=pair["tool"],
            repo=pair["repo"],
            transcript_path=pair["transcript_path"],
            scenario_path=pair["scenario_path"],
            rubric_path=pair["rubric_path"],
            out_dir=work_dir,
            force=args.force,
        )
        per_pair[key] = judged

    out = write_validation(args.iter_dir, per_pair)
    print(f"[heldout_rescore] wrote {out}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.path.insert(0, LIB_DIR)
    sys.exit(main())
