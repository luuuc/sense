#!/usr/bin/env python3
"""Reference-aware relationship audit for the judge layer.

The per-step LLM judge scores an answer in isolation: it has no reference for
what a COMPLETE answer must contain, so it rates a confident-but-incomplete
answer as complete (on chatwoot it called a 35%-recall dependent audit
"exhaustive"). That blindness to omission is why the reference-blind judge never
separated the arms even though objective gold recall did (0.35 vs 0.94).

This module fixes that. Given a scenario's gold targets that carry a `relation`
(the TRUE way each reaches the contract under change — authored from source,
never shown to the agent), it asks the judge to grade an answer against that
fixed reference set, per item:

  covered — the answer explicitly names the file / class / symbol.
  related — covered AND the answer states a connection consistent with the
            item's true relation (not just the endpoint).

  covered_recall = covered / N      (tracks mention-recall; penalises omission)
  related_recall = related / N      (chain correctness: grep can name endpoints,
                                     it cannot assert the correct relation)

Scored identically for both arms against the same reference, so it cannot be
gamed toward either. The reference is never rendered to the tool-under-test.
"""

from __future__ import annotations

import json


SYSTEM = "You are a precise grader. You output only a single JSON object."

_INSTRUCTIONS = """You are grading whether an ANSWER covered a fixed REFERENCE SET of dependents of the {subject}, and whether it stated the CORRECT relationship for each.

Each reference item has an id, where it lives, and the TRUE relation by which it reaches the contract under change. Judge EACH reference item independently against the answer AS WRITTEN:

- "covered": true ONLY if the answer explicitly names that file, or its class / symbol (a location pin or the concrete name). If the answer never mentions it, covered=false.
- "related": true ONLY if covered AND the answer states a connection to the contract CONSISTENT with the item's true relation (the association / concern / polymorphic relation, or the role it plays). Naming the file with no stated connection, or asserting a wrong relation, is related=false.

Do not give credit for items the answer does not actually contain. Do not infer coverage from the reference set itself. A long, confident answer that omits an item still scores covered=false for that item.

Output a single JSON object and nothing else:
{{"items": [{{"id": "<id>", "covered": <bool>, "related": <bool>}}, ...]}}
one entry per reference item, ids verbatim.

REFERENCE SET:
"""


def build_reference(gold):
    """[(id, where, relation)] for gold targets that declare a `relation`.

    `where` is the first file-like match pattern (a hint for the grader about
    which file the item is), falling back to the id. Targets without a relation
    are skipped — the audit only covers the authored reference.
    """
    ref = []
    for item in gold or []:
        if isinstance(item, str):
            continue
        relation = item.get("relation")
        if not relation:
            continue
        rid = item.get("id") or (item.get("match") or ["?"])[0]
        where = (item.get("match") or [rid])[0]
        ref.append((rid, str(where), str(relation)))
    return ref


def build_user_prompt(answer_text, reference, subject):
    lines = [_INSTRUCTIONS.format(subject=subject)]
    for rid, where, relation in reference:
        lines.append(f"- id={rid} | where={where} | true_relation: {relation}")
    lines.append("\nANSWER:\n")
    lines.append(answer_text or "(empty — the tool produced no answer)")
    return "\n".join(lines)


def _coerce_bool(v):
    if isinstance(v, bool):
        return v
    if isinstance(v, str):
        return v.strip().lower() in ("true", "yes", "1")
    return bool(v)


def grade(answer_text, gold, *, call_judge, extract_json, subject="contract under change",
          api_key=None):
    """Run the reference-aware relationship audit on one answer.

    `call_judge(system_text, user_text, *, api_key)` and
    `extract_json(api_response)` are injected (judge.py passes its own, so this
    module does not import judge — avoiding a cycle). Returns None when no gold
    target carries a `relation`.
    """
    reference = build_reference(gold)
    if not reference:
        return None
    n = len(reference)
    user_text = build_user_prompt(answer_text, reference, subject)
    response = call_judge(SYSTEM, user_text, api_key=api_key)
    parsed = extract_json(response)

    by_id = {}
    for it in parsed.get("items", []) or []:
        rid = it.get("id")
        if rid is not None:
            by_id[rid] = it

    items, covered, related = [], 0, 0
    missed_covered, missed_related = [], []
    for rid, _where, _rel in reference:
        it = by_id.get(rid, {})
        c = _coerce_bool(it.get("covered"))
        r = c and _coerce_bool(it.get("related"))
        items.append({"id": rid, "covered": c, "related": r})
        covered += 1 if c else 0
        related += 1 if r else 0
        if not c:
            missed_covered.append(rid)
        if not r:
            missed_related.append(rid)

    return {
        "total": n,
        "covered": covered,
        "related": related,
        "covered_recall": round(covered / n, 4) if n else 0.0,
        "related_recall": round(related / n, 4) if n else 0.0,
        "missed_covered": missed_covered,
        "missed_related": missed_related,
        "items": items,
    }
