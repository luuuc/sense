#!/usr/bin/env python3
"""Gold-target recall: did the answer surface the references a correct answer must contain?

A scenario may declare a `gold` list — the symbols / files a correct answer is
required to surface. Recall is scored against the SAME denominator for every
arm, which citation_grounding cannot do (it is grounded/emitted, a different
denominator per arm).

Two strictness levels, because they tell different stories:
  - mention_recall — the target's name/path appears at all (completeness of the
                     map). On RubyLLM both arms hit 13/13 providers here.
  - cited_recall   — the target is pinned to an exact `path:line` (precision of
                     the map: an agent can jump straight there instead of
                     grepping). This is where a structural tool pulls ahead —
                     RubyLLM baseline 3/13, sense 13/13.

Each gold item (a dict, or a bare string shorthand):
  - id:    human label, unique within the scenario
  - group: optional bucket for per-group recall (e.g. "providers")
  - match: list of case-insensitive substrings; the target is MENTIONED if any
           appears. A pattern that looks like a file (has "/" and "." or a code
           extension) also drives the CITED check: the target is cited when that
           path is followed by `:<line>` in the answer. Items with no file-like
           pattern (pure symbol names) treat cited == mentioned.

Returns None when the scenario declares no gold — older scenarios and
competitor runs are unaffected.
"""

import re

_CODE_EXT = (".rb", ".py", ".go", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java",
             ".kt", ".cs", ".cpp", ".cc", ".c", ".php", ".scala", ".erb")


def _is_file_like(p):
    pl = p.lower()
    return pl.endswith(_CODE_EXT) or ("/" in p and "." in p)


def _cited(pattern, hay):
    # path immediately followed by a line locator, e.g. "providers/anthropic.rb:24"
    return bool(re.search(re.escape(pattern.lower()) + r":\d+", hay))


def score_gold_recall(answer_text, gold):
    if not gold:
        return None
    hay = (answer_text or "").lower()
    details, groups = [], {}
    men_total = cit_total = 0
    for item in gold:
        if isinstance(item, str):
            item = {"id": item, "match": [item]}
        gid = item.get("id") or (item.get("match") or ["?"])[0]
        grp = item.get("group", "_")
        pats = item.get("match") or [gid]
        mentioned = any(str(p).lower() in hay for p in pats)
        file_pats = [p for p in pats if _is_file_like(str(p))]
        if file_pats:
            cited = any(_cited(str(p), hay) for p in file_pats)
        else:
            cited = mentioned  # pure symbol target: mention is the best we can verify
        men_total += 1 if mentioned else 0
        cit_total += 1 if cited else 0
        details.append({"id": gid, "group": grp, "mentioned": mentioned, "cited": cited})
        g = groups.setdefault(grp, {"total": 0, "mentioned": 0, "cited": 0})
        g["total"] += 1
        g["mentioned"] += 1 if mentioned else 0
        g["cited"] += 1 if cited else 0
    n = len(details)
    for g in groups.values():
        g["mention_recall"] = round(g["mentioned"] / g["total"], 4) if g["total"] else 0.0
        g["cited_recall"] = round(g["cited"] / g["total"], 4) if g["total"] else 0.0
    return {
        "total": n,
        "mentioned": men_total,
        "cited": cit_total,
        "mention_recall": round(men_total / n, 4) if n else 0.0,
        "cited_recall": round(cit_total / n, 4) if n else 0.0,
        "groups": groups,
        "missed_mention": [d["id"] for d in details if not d["mentioned"]],
        "missed_cite": [d["id"] for d in details if not d["cited"]],
        "details": details,
    }
