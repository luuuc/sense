#!/usr/bin/env python3
"""Citation grounding for bench.

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

import functools
import os
import re
from typing import List, Tuple

from scorer import _SOURCE_FILE_RE


# ── Repo-aware path resolution ───────────────────────────────────────
#
# LLM citations are not consistent about path depth. The model might write
# `app.py:1566` when it means `src/flask/app.py:1566`, or `flask/app.py`
# from a deeper-nested project. Treating those as unresolved is a false
# negative — the citation isn't a hallucination, just abbreviated.
#
# The resolver tries the literal path first (the common case for sense's
# `ref` fields, which include the full repo-relative path) and falls back
# to a uniqueness check via the repo's file suffix map.


@functools.lru_cache(maxsize=8)
def _index_repo_files(repo_path: str) -> Tuple[Tuple[str, ...], dict]:
    """Walk repo_path once and build (all_rel_paths, basename_map).

    basename_map[name] = tuple of rel paths whose final segment is `name`.
    Caches per repo_path because ground_citations() may call us 30+ times
    per transcript, and the discourse/nextjs walks are 100k+ files.
    Excludes obvious noise dirs (.git, .sense, node_modules, vendor, etc.).
    """
    skip_dirs = {".git", ".sense", ".serena", ".gitnexus", ".roam",
                 ".grepai", "node_modules", "__pycache__", "vendor",
                 ".bundle", ".workspace", "target", "build", "dist"}
    all_files: list[str] = []
    basename_map: dict[str, list[str]] = {}
    for root, dirs, files in os.walk(repo_path):
        dirs[:] = [d for d in dirs if d not in skip_dirs and not d.startswith(".")]
        for f in files:
            rel = os.path.relpath(os.path.join(root, f), repo_path)
            all_files.append(rel)
            basename_map.setdefault(f, []).append(rel)
    return tuple(all_files), {k: tuple(v) for k, v in basename_map.items()}


def _resolve_path(file: str, locator: str, repo_path: str) -> Tuple[str | None, str]:
    """Resolve a cited path against the repo.

    Returns (rel_path, note). rel_path is None when resolution fails;
    note explains why (for the citation's `reason` field).

    Resolution strategy:
      1. Literal: $repo/$file exists → done.
      2. Suffix-match: find files whose path ends with `file` (or whose
         basename equals it when there's no slash). One candidate → done.
      3. Tie-break by line: when locator is a line number, drop candidates
         that are too short to contain it. If exactly one survives, done.
         (Flask has `app.py` in src/flask, src/flask/sansio, and a tiny
         test fixture — line 1501 fits only the first.)
    """
    literal = os.path.join(repo_path, file)
    if os.path.isfile(literal):
        return file, ""

    all_files, basename_map = _index_repo_files(repo_path)

    if "/" not in file:
        candidates = basename_map.get(file, ())
    else:
        sep = "/" + file
        candidates = tuple(f for f in all_files if f.endswith(sep))

    if not candidates:
        return None, f"file not found at {file}"
    if len(candidates) == 1:
        return candidates[0], ""

    line_num, _ = _classify_locator(locator)
    if line_num is not None:
        # Drop candidates whose file is too short to contain the cited line.
        viable: list[Tuple[str, int]] = []
        for c in candidates:
            try:
                with open(os.path.join(repo_path, c), "r",
                          encoding="utf-8", errors="replace") as fh:
                    n = sum(1 for _ in fh)
                if n >= line_num:
                    viable.append((c, n))
            except OSError:
                pass
        if len(viable) == 1:
            return viable[0][0], ""
        if len(viable) > 1:
            # Multiple files contain the line. The cited method/line is
            # typically in the "main" file (most lines), not a smaller
            # derivative — `src/flask/app.py` over `src/flask/sansio/app.py`.
            # Documented heuristic, not principled — but matches practice.
            viable.sort(key=lambda t: -t[1])
            return viable[0][0], ""

    head = ", ".join(candidates[:3]) + ("..." if len(candidates) > 3 else "")
    return None, f"ambiguous: {len(candidates)} files match `{file}` ({head})"


# ── Extraction ───────────────────────────────────────────────────────


def extract_citations(answer_text: str) -> List[Tuple[str, str]]:
    """Return de-duplicated (file, locator) pairs in order of first appearance.

    Reuses the scorer's _SOURCE_FILE_RE so the set of citations counted
    here is the same set that drives response_richness. Anything that
    regex misses is by definition not a "citation" for bench purposes.
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
    resolved, resolve_note = _resolve_path(file, locator, repo_path)
    if resolved is None:
        return {
            "file": file,
            "locator": locator,
            "status": "unresolved",
            "reason": resolve_note,
        }

    full = os.path.join(repo_path, resolved)
    line_num, symbol = _classify_locator(locator)
    # `via` is appended to grounded reasons when we matched by suffix rather
    # than literal path, so the scored.json reader sees that `app.py` was
    # actually resolved to `src/flask/app.py`.
    via = f" [via {resolved}]" if resolved != file else ""

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
                "reason": f"line {line_num} in {file} (file has {n_lines} lines){via}",
            }
        return {
            "file": file,
            "locator": locator,
            "status": "hallucinated",
            "reason": f"line {line_num} out of range (file only {n_lines} lines){via}",
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
                "reason": f"line {line_num} out of range (file only {n_lines} lines){via}",
            }
        lo = max(1, line_num - 5)
        hi = min(n_lines, line_num + 5)
        window = "".join(lines[lo - 1:hi])
        if pattern.search(window):
            return {
                "file": file,
                "locator": locator,
                "status": "grounded",
                "reason": f"`{symbol}` found near line {line_num} in {file}{via}",
            }
        return {
            "file": file,
            "locator": locator,
            "status": "unresolved",
            "reason": f"`{symbol}` not within ±5 of line {line_num} in {file}{via}",
        }

    # No line hint — grep the whole file.
    body = "".join(lines)
    if pattern.search(body):
        return {
            "file": file,
            "locator": locator,
            "status": "grounded",
            "reason": f"`{symbol}` found in {file}{via}",
        }
    return {
        "file": file,
        "locator": locator,
        "status": "unresolved",
        "reason": f"`{symbol}` not found anywhere in {file}{via}",
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
