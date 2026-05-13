#!/usr/bin/env python3
"""Citation grounding for bench2.

Pulls `file.ext:locator` references out of an assistant's answer_text and
checks each one against the repo checked out at run time. A citation is:

  grounded     — file exists and the locator resolves (line in range, or
                 symbol found within ±5 lines of the cited line, or
                 anywhere in the file if no line was given).
  hallucinated — file exists but the locator is impossible (line beyond
                 EOF). This is the hard "I made up a number" signal.
  unresolved   — file is missing, or symbol can't be found near the
                 cited line. Softer: the LLM may be on the right track
                 in the wrong commit, or naming a real symbol slightly
                 off. Not penalised, but worth surfacing.

Naive by design: no tree-sitter, no fuzzy matching, no graph awareness.
The pitch (20-04) is explicit that ±5 line word-boundary grep is the bar.
"""

from __future__ import annotations

import os
import re
from typing import List, Tuple

from scorer import _SOURCE_FILE_RE


# ── Extraction ───────────────────────────────────────────────────────


def extract_citations(answer_text: str) -> List[Tuple[str, str]]:
    """Return de-duplicated (file, locator) pairs in order of first appearance.

    Reuses the scorer's _SOURCE_FILE_RE so the set of citations counted
    here is the same set that drives response_richness. Anything that
    regex misses is by definition not a "citation" for bench2 purposes.
    """
    seen = set()
    out: List[Tuple[str, str]] = []
    for file, locator in _SOURCE_FILE_RE.findall(answer_text):
        key = (file, locator)
        if key in seen:
            continue
        seen.add(key)
        out.append(key)
    return out


# ── Locator classification ───────────────────────────────────────────


def _classify_locator(locator: str):
    """Split a locator into (line_number, symbol).

    Examples:
      "123"            → (123, None)            line citation
      "Foo"            → (None, "Foo")          symbol citation
      "Class#method"   → (None, "method")       trailing identifier
      "Class.method"   → (None, "method")       trailing identifier
      "Foo:42"         → (42, "Foo")            symbol with line hint

    Heuristic: pick the trailing identifier as the symbol to grep for.
    A bare class name is rarely cited on its own — when the LLM says
    "Class#method" the *method* is what we want to verify exists near
    the cited line. Class context is implied by the file path.
    """
    if locator.isdigit():
        return int(locator), None

    tokens = re.findall(r"\w+", locator)
    line_num = None
    if tokens and tokens[-1].isdigit():
        line_num = int(tokens[-1])
        tokens = tokens[:-1]

    if not tokens:
        return line_num, None

    return line_num, tokens[-1]


# ── Per-citation grounding ───────────────────────────────────────────


def ground_citation(file: str, locator: str, repo_path: str) -> dict:
    """Check one citation against the repo.

    Returns a dict shaped like:
      {"file": ..., "locator": ..., "status": "grounded"|"unresolved"|"hallucinated",
       "reason": "one-line explanation"}
    """
    full = os.path.join(repo_path, file)
    if not os.path.exists(full) or not os.path.isfile(full):
        return {
            "file": file,
            "locator": locator,
            "status": "unresolved",
            "reason": f"file not found at {file}",
        }

    line_num, symbol = _classify_locator(locator)

    try:
        with open(full, "r", encoding="utf-8", errors="replace") as f:
            lines = f.readlines()
    except OSError as e:
        return {
            "file": file,
            "locator": locator,
            "status": "unresolved",
            "reason": f"could not read {file}: {e}",
        }

    n_lines = len(lines)

    # Pure line citation: grounded iff in range; otherwise hallucinated.
    if symbol is None:
        if line_num is None:
            # locator was something like "_" with no usable identifier.
            return {
                "file": file,
                "locator": locator,
                "status": "unresolved",
                "reason": "locator had no line or symbol",
            }
        if 1 <= line_num <= n_lines:
            return {
                "file": file,
                "locator": locator,
                "status": "grounded",
                "reason": f"line {line_num} in {file} (file has {n_lines} lines)",
            }
        return {
            "file": file,
            "locator": locator,
            "status": "hallucinated",
            "reason": f"line {line_num} out of range (file only {n_lines} lines)",
        }

    # Symbol citation: word-boundary search.
    pattern = re.compile(r"\b" + re.escape(symbol) + r"\b")

    if line_num is not None:
        if not (1 <= line_num <= n_lines):
            # The line number is itself impossible — hard hallucination,
            # regardless of whether the symbol exists somewhere else.
            return {
                "file": file,
                "locator": locator,
                "status": "hallucinated",
                "reason": f"line {line_num} out of range (file only {n_lines} lines)",
            }
        lo = max(1, line_num - 5)
        hi = min(n_lines, line_num + 5)
        window = "".join(lines[lo - 1:hi])
        if pattern.search(window):
            return {
                "file": file,
                "locator": locator,
                "status": "grounded",
                "reason": f"`{symbol}` found near line {line_num} in {file}",
            }
        return {
            "file": file,
            "locator": locator,
            "status": "unresolved",
            "reason": f"`{symbol}` not within ±5 of line {line_num} in {file}",
        }

    # No line hint — grep the whole file.
    body = "".join(lines)
    if pattern.search(body):
        return {
            "file": file,
            "locator": locator,
            "status": "grounded",
            "reason": f"`{symbol}` found in {file}",
        }
    return {
        "file": file,
        "locator": locator,
        "status": "unresolved",
        "reason": f"`{symbol}` not found anywhere in {file}",
    }


# ── Aggregation ──────────────────────────────────────────────────────


_EXAMPLES_PER_BUCKET = 5


def ground_citations(answer_text: str, repo_path: str | None) -> dict:
    """Return the `citation_grounding` block for a scored.json.

    If repo_path is missing or not a directory, grounding is reported
    as 0/0 with a `skipped` reason — the rest of the score should still
    be usable.
    """
    citations = extract_citations(answer_text)

    if not repo_path or not os.path.isdir(repo_path):
        return {
            "total": len(citations),
            "grounded": 0,
            "unresolved": 0,
            "hallucinated": 0,
            "rate": 0.0,
            "skipped": "repo_path missing or not a directory",
            "examples": {"grounded": [], "unresolved": [], "hallucinated": []},
            "details": [],
        }

    details = [
        ground_citation(file, locator, repo_path) for file, locator in citations
    ]

    counts = {"grounded": 0, "unresolved": 0, "hallucinated": 0}
    examples = {"grounded": [], "unresolved": [], "hallucinated": []}
    for d in details:
        s = d["status"]
        counts[s] += 1
        if len(examples[s]) < _EXAMPLES_PER_BUCKET:
            examples[s].append(f"{d['file']}:{d['locator']} ({d['reason']})")

    total = len(details)
    grounded = counts["grounded"]
    rate = (grounded / total) if total > 0 else 0.0

    return {
        "total": total,
        "grounded": grounded,
        "unresolved": counts["unresolved"],
        "hallucinated": counts["hallucinated"],
        "rate": round(rate, 4),
        "examples": examples,
        "details": details,
    }
