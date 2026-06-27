#!/usr/bin/env python3
"""parse-claude-result.py — extract cost/tokens/wall_time from a
stream-json `claude -p ... --output-format stream-json` log file and
write a single-object JSON suitable for scorer.py overhead folding.

Usage: parse-claude-result.py <log_path> <out_path> <fallback_wall_secs> <repo>

The log is a sequence of JSON objects, one per line. The last `result`
event carries the canonical totals; if it never fired (truncation), we
fall back to summing per-message `usage` blocks and report zero cost.
"""
from __future__ import annotations

import json
import os
import sys


def main() -> int:
    if len(sys.argv) != 5:
        print(__doc__, file=sys.stderr)
        return 2

    log_path, out_path, fallback_wall_s, repo = sys.argv[1:5]
    fallback_wall_s = float(fallback_wall_s)

    last_result: dict | None = None
    msg_usage = {
        "input_tokens": 0,
        "output_tokens": 0,
        "cache_read_input_tokens": 0,
        "cache_creation_input_tokens": 0,
    }

    if os.path.exists(log_path):
        with open(log_path) as fh:
            for line in fh:
                line = line.strip()
                if not line.startswith("{"):
                    continue
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if obj.get("type") == "result":
                    last_result = obj
                elif obj.get("type") == "assistant":
                    u = (obj.get("message") or {}).get("usage") or {}
                    for k in msg_usage:
                        msg_usage[k] += u.get(k, 0) or 0

    if last_result is not None:
        usage = last_result.get("usage") or {}
        wall_s = (last_result.get("duration_ms") or 0) / 1000.0 or fallback_wall_s
        cost = float(last_result.get("total_cost_usd") or 0.0)
        num_turns = last_result.get("num_turns")
    else:
        usage = msg_usage
        wall_s = fallback_wall_s
        cost = 0.0
        num_turns = None

    out = {
        "source": "serena_onboarding",
        "repo": repo,
        "wall_time_seconds": round(wall_s, 1),
        "token_input_uncached": usage.get("input_tokens", 0),
        "token_output": usage.get("output_tokens", 0),
        "token_cache_read": usage.get("cache_read_input_tokens", 0),
        "token_cache_write": usage.get("cache_creation_input_tokens", 0),
        "token_total_billed": (usage.get("input_tokens", 0)
                               + usage.get("output_tokens", 0)),
        "cost_usd": round(cost, 4),
        "num_turns": num_turns,
        "had_result_event": last_result is not None,
    }
    with open(out_path, "w") as fh:
        json.dump(out, fh, indent=2)
        fh.write("\n")

    print(
        f"[overhead] {repo}: ${out['cost_usd']:.4f} | "
        f"{out['token_total_billed']} billed tok | "
        f"{out['wall_time_seconds']:.1f}s",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
