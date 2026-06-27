"""Cost tracking + ceiling enforcement for the improvement loop.

Cost in bench is a *comparability* metric, not an accounting one. Every
call's cost is computed from public API token rates regardless of whether
it was actually billed through ANTHROPIC_API_KEY or via OAuth subscription.
A token is a token. Recording subscription-mode discounts would make iter-N
look artificially cheap vs iter-(N-1) and the cost-trend would lie.

What we count:
  - Scenario sessions: `scored.json.cost_usd` (computed by scorer.py from
    the transcript's total_cost_usd, or estimated from partial usage if
    the session failed before reporting final cost).
  - Judge calls: `judged.json.steps[].usage` × PRICE_PER_M.
  - Audit calls: `audit-scoring.json.usage`, `audit-scenarios.json.usage`,
    `audit-watchdog.json.usage` × PRICE_PER_M.

Writing:
  - `loop-N/cost.json` — cumulative across iterations.
  - Per-iteration breakdown under `iterations[].`

CLI:
  python3 bench/lib/cost_tracker.py update --loop-dir DIR --iter N
  python3 bench/lib/cost_tracker.py predict --loop-dir DIR \\
      [--max-cost-usd 10] [--first-iter-prior 12]
      → exits 0 if next-iter estimate fits under ceiling, 10 otherwise.
"""

from __future__ import annotations

import argparse
import glob
import json
import os
import sys
from typing import Any

LIB_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, LIB_DIR)
from scorer import PRICE_PER_M, estimate_cost  # noqa: E402


# --- IO helpers ------------------------------------------------------------

def _load(path: str) -> Any:
    with open(path) as f:
        return json.load(f)


def _maybe_load(path: str) -> Any | None:
    return _load(path) if os.path.exists(path) else None


def _zero_usage() -> dict[str, int]:
    return {
        "input_tokens": 0,
        "output_tokens": 0,
        "cache_creation_input_tokens": 0,
        "cache_read_input_tokens": 0,
    }


def _add(into: dict[str, int], more: dict[str, Any]) -> None:
    for k in into:
        into[k] += int(more.get(k, 0) or 0)


# --- Aggregators -----------------------------------------------------------

def session_costs_for_loop(bench_results_dir: str) -> dict[str, Any]:
    """Sum `cost_usd` across every scored.json under bench/results/.

    Cost lives at `scored.json.metrics.cost_usd` (scorer.score_transcript
    nests session metrics into the `metrics` sub-dict). Top-level
    `cost_usd` is a legacy/unused field; reading it returns None for
    every modern run.

    This represents scenario-session spend (the bulk of cost). One number
    per iteration is fine — sessions are cumulative across the bench's
    results dir, not partitioned per iter (the loop re-runs in place).
    """
    paths = sorted(glob.glob(os.path.join(bench_results_dir, "**", "scored.json"), recursive=True))
    total = 0.0
    counted = 0
    for path in paths:
        try:
            d = _load(path)
        except (OSError, json.JSONDecodeError):
            continue
        # Prefer metrics.cost_usd (current); fall back to top-level for very old runs.
        m = d.get("metrics") or {}
        c = m.get("cost_usd") if m.get("cost_usd") is not None else d.get("cost_usd")
        if c is not None:
            total += float(c)
            counted += 1
    return {"cost_usd": round(total, 4), "scored_files": counted}


def judge_costs_for_loop(bench_results_dir: str) -> dict[str, Any]:
    """Sum judge cost across every judged.json (per-step usage × PRICE)."""
    paths = sorted(glob.glob(os.path.join(bench_results_dir, "**", "judged.json"), recursive=True))
    usage = _zero_usage()
    files = 0
    for path in paths:
        try:
            d = _load(path)
        except (OSError, json.JSONDecodeError):
            continue
        for step in d.get("steps", []) or []:
            u = step.get("usage") or {}
            _add(usage, u)
        files += 1
    return {
        "cost_usd": round(estimate_cost(usage), 4),
        "usage": usage,
        "judged_files": files,
    }


def audit_costs_for_iter(iter_dir: str) -> dict[str, Any]:
    """Sum audit cost from every audit-*.json in the iter directory.

    Audit files are written per-iteration (Phase 4), so this is the right
    granularity. Skips *-full.json variants — they share the same usage
    as their summary counterpart, just expanded.
    """
    usage = _zero_usage()
    files = 0
    for fname in sorted(os.listdir(iter_dir)):
        if not fname.startswith("audit-"):
            continue
        if not fname.endswith(".json") or fname.endswith("-full.json"):
            continue
        path = os.path.join(iter_dir, fname)
        try:
            d = _load(path)
        except (OSError, json.JSONDecodeError):
            continue
        u = d.get("usage") or {}
        _add(usage, u)
        files += 1
    return {
        "cost_usd": round(estimate_cost(usage), 4),
        "usage": usage,
        "audit_files": files,
    }


# --- Persistence -----------------------------------------------------------

def update_cost_json(
    loop_dir: str, iter_n: int, bench_results_dir: str
) -> dict[str, Any]:
    """Recompute `loop_dir/cost.json` for iter_n.

    Each iteration writes its own block under `iterations`. `cumulative`
    is the sum of every block's `iter_cost_usd`.
    """
    iter_dir = os.path.join(loop_dir, f"loop-1-iter-{iter_n}")
    cost_path = os.path.join(loop_dir, "loop-1", "cost.json")
    os.makedirs(os.path.dirname(cost_path), exist_ok=True)

    sess = session_costs_for_loop(bench_results_dir)
    judge = judge_costs_for_loop(bench_results_dir)
    audit = audit_costs_for_iter(iter_dir) if os.path.isdir(iter_dir) else {
        "cost_usd": 0.0, "usage": _zero_usage(), "audit_files": 0,
    }

    iter_block = {
        "iter": iter_n,
        # sessions + judge are cumulative across the bench's results dir, not
        # easily partitioned. We attribute them to the most recent iter that
        # ran them and zero them out for prior iters by delta-ing on update.
        "sessions": sess,
        "judge": judge,
        "audit": audit,
        "iter_cost_usd": round(
            sess["cost_usd"] + judge["cost_usd"] + audit["cost_usd"], 4
        ),
    }

    existing = _maybe_load(cost_path) or {"iterations": []}

    # Replace any prior block for this iter (idempotent) and re-derive
    # cumulative as max(iter_cost_usd seen so far) — sessions+judge are
    # cumulative, audit is per-iter, so the cumulative number = the
    # highest iter's sessions+judge plus the *sum* of every iter's audit.
    existing["iterations"] = [
        b for b in existing["iterations"] if b.get("iter") != iter_n
    ] + [iter_block]
    existing["iterations"].sort(key=lambda b: b["iter"])

    cumulative_audit = sum(b["audit"]["cost_usd"] for b in existing["iterations"])
    latest = max(existing["iterations"], key=lambda b: b["iter"])
    cumulative = round(
        latest["sessions"]["cost_usd"] + latest["judge"]["cost_usd"] + cumulative_audit, 4
    )
    existing["cumulative_usd"] = cumulative

    with open(cost_path, "w") as f:
        json.dump(existing, f, indent=2)
        f.write("\n")

    return existing


def estimate_next_iter_cost(
    cost_state: dict[str, Any], first_iter_prior: float = 3.0
) -> float:
    """Predict the next iter's cost as a copy of the last iter's
    `iter_cost_usd`, falling back to `first_iter_prior` if no iters yet.

    Note: sessions+judge components are bench-wide cumulative numbers, so
    "last iter's spend" is a slight over-estimate (we'd be charging this
    iter for spend that mostly happened earlier). That's the safe side of
    the trade-off for a hard ceiling.
    """
    iters = cost_state.get("iterations", [])
    if not iters:
        return float(first_iter_prior)
    return float(iters[-1]["iter_cost_usd"])


def should_halt(
    cost_state: dict[str, Any],
    max_cost_usd: float,
    first_iter_prior: float = 3.0,
) -> tuple[bool, str]:
    """Return (halt, reason). Halt if cumulative + predicted > ceiling."""
    cumulative = float(cost_state.get("cumulative_usd", 0.0))
    predicted = estimate_next_iter_cost(cost_state, first_iter_prior)
    if cumulative + predicted > max_cost_usd:
        return True, (
            f"cumulative ${cumulative:.2f} + predicted next ${predicted:.2f} "
            f"= ${cumulative + predicted:.2f} > ceiling ${max_cost_usd:.2f}"
        )
    return False, (
        f"cumulative ${cumulative:.2f} + predicted next ${predicted:.2f} "
        f"= ${cumulative + predicted:.2f} ≤ ceiling ${max_cost_usd:.2f}"
    )


# --- CLI -------------------------------------------------------------------

def _cmd_update(args) -> int:
    state = update_cost_json(args.loop_dir, args.iter, args.bench_results_dir)
    print(
        f"cost: iter={args.iter} iter_cost=${state['iterations'][-1]['iter_cost_usd']:.2f} "
        f"cumulative=${state['cumulative_usd']:.2f}",
        file=sys.stderr,
    )
    return 0


def _cmd_predict(args) -> int:
    cost_path = os.path.join(args.loop_dir, "loop-1", "cost.json")
    state = _maybe_load(cost_path) or {"iterations": [], "cumulative_usd": 0.0}
    halt, reason = should_halt(state, args.max_cost_usd, args.first_iter_prior)
    print(reason, file=sys.stderr)
    return 10 if halt else 0


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = p.add_subparsers(dest="cmd", required=True)

    p_u = sub.add_parser("update", help="recompute cost.json after an iter")
    p_u.add_argument("--loop-dir", required=True, help="directory with per-iteration loop results")
    p_u.add_argument("--iter", type=int, required=True)
    p_u.add_argument(
        "--bench-results-dir",
        required=True,
        help="bench/results (for scored.json + judged.json)",
    )
    p_u.set_defaults(func=_cmd_update)

    p_p = sub.add_parser("predict", help="halt-before-overspend predicate")
    p_p.add_argument("--loop-dir", required=True)
    p_p.add_argument("--max-cost-usd", type=float, default=10.0)
    p_p.add_argument("--first-iter-prior", type=float, default=12.0)
    p_p.set_defaults(func=_cmd_predict)

    args = p.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
