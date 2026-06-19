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

  covered      — the answer explicitly names the file / class / symbol.
  related      — covered AND the answer states a connection consistent with the
                 item's true relation (not just the endpoint).
  contradicted — covered AND the answer asserts a relation/role for the item that
                 CONTRADICTS its true relation (a confident but FALSE claim, e.g.
                 calling a non-rendering parser a "renderer"). This is the
                 anti-fabrication signal: it is strictly worse than omission.

  covered_recall    = covered / N        (mention-recall; penalises omission)
  related_recall    = related / N        (chain correctness: grep can name
                                          endpoints, it cannot assert the relation)
  grounded_precision = 1 - contradicted/covered
                                         (of what the answer DID characterise, the
                                          fraction it characterised truthfully —
                                          a frontier baseline that confidently
                                          mislabels a dependent is penalised here
                                          where the omission-blind judge rewarded it)

Scored identically for both arms against the same reference, so it cannot be
gamed toward either. The reference is never rendered to the tool-under-test.

Fix 3 (semantic grounding / anti-fabrication, Judging Contract rule 4): the
`contradicted` tri-state is the authored-relation realisation of "verify the
stated relation, penalise confident-false". It needs no code snippet or extra
judge call — the gold `relation` IS the ground truth, folded into the one
existing reference-aware call.
"""

from __future__ import annotations

import json


SYSTEM = "You are a precise grader. You output only a single JSON object."

_INSTRUCTIONS = """You are grading whether an ANSWER covered a fixed REFERENCE SET of dependents of the {subject}, and whether it stated the CORRECT relationship for each.

Each reference item has an id, where it lives, and the TRUE relation by which it reaches the contract under change. Judge EACH reference item independently against the answer AS WRITTEN:

- "covered": true ONLY if the answer explicitly names that file, or its class / symbol (a location pin or the concrete name). If the answer never mentions it, covered=false.
- "related": true ONLY if covered AND the answer states a connection to the contract CONSISTENT with the item's true relation (the association / concern / polymorphic relation, or the role it plays). Naming the file with no stated connection is related=false (but NOT contradicted).
- "contradicted": true ONLY if covered AND the answer ASSIGNS THIS item a specific role/behaviour that is FACTUALLY INCOMPATIBLE with its true relation — a stated WRONG characterisation (e.g. the true relation is a "non-rendering parser that derives a reference" and the answer says it "renders article markdown"; or the answer claims an inline effect for something the relation says runs async). The wrong role must be ASSERTED for this item — including a label/heading that plainly applies to it (the line "liquid_embed_extractor.rb — render article markdown" asserts the wrong "render" role). It is NOT a contradiction when the answer merely LISTS the file among others with no role asserted to it, or groups it under a broad/neutral heading that does not pin a specific incompatible role — that is related=false (omitted relation), not contradicted. A contradicted item is never also related. When in doubt whether the answer ASSERTED a wrong role versus simply did not state the role, choose related=false, NOT contradicted.

Do not give credit for items the answer does not actually contain. Do not infer coverage from the reference set itself. A long, confident answer that omits an item still scores covered=false for that item. Reserve contradicted for a wrong role the answer actually ASSERTS about the item — silence, bare enumeration, and vagueness are omission (related=false), never contradiction.

If an item carries a "verified_fact:", treat it as ground truth about the code, confirmed from source: it states what the item IS and often what it is NOT. Judge `related`/`contradicted` against it — an answer consistent with it is related; an answer that ASSERTS what the verified_fact rules out is contradicted; an answer that simply omits the role stays related=false (not contradicted).

Output a single JSON object and nothing else:
{{"items": [{{"id": "<id>", "covered": <bool>, "related": <bool>, "contradicted": <bool>}}, ...]}}
one entry per reference item, ids verbatim.

REFERENCE SET:
"""


def build_reference(gold):
    """[(id, where, relation, disambiguator)] for gold targets with a `relation`.

    `where` is the first file-like match pattern (a hint for the grader about
    which file the item is), falling back to the id. Targets without a relation
    are skipped — the audit only covers the authored reference.

    `disambiguator` is an OPTIONAL verified-from-source note (the fact channel):
    a fact about the CODE that prevents a known confusion, e.g. "NOT a URL
    rewriter". It is rendered to the judge alongside the relation so a fact we
    have proved is shared with the judge and not re-litigated. Empty string when
    the gold item declares none. Authored from source, symmetric, never shown to
    the agent — see .doc/launch/00-judging-contract.md.
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
        note = item.get("disambiguator") or ""
        ref.append((rid, str(where), str(relation), str(note)))
    return ref


def build_user_prompt(answer_text, reference, subject):
    lines = [_INSTRUCTIONS.format(subject=subject)]
    for rid, where, relation, note in reference:
        line = f"- id={rid} | where={where} | true_relation: {relation}"
        if note:
            line += f" | verified_fact: {note}"
        lines.append(line)
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

    items, covered, related, contradicted = [], 0, 0, 0
    missed_covered, missed_related, contradictions = [], [], []
    for rid, _where, _rel, _note in reference:
        it = by_id.get(rid, {})
        c = _coerce_bool(it.get("covered"))
        # A contradiction requires the item to be covered (a claim was made) and
        # is mutually exclusive with `related` — you cannot both assert the
        # correct relation and contradict it. Contradiction wins the tie so a
        # confident-false claim can never be laundered into a `related` credit.
        x = c and _coerce_bool(it.get("contradicted"))
        r = c and (not x) and _coerce_bool(it.get("related"))
        items.append({"id": rid, "covered": c, "related": r, "contradicted": x})
        covered += 1 if c else 0
        related += 1 if r else 0
        contradicted += 1 if x else 0
        if not c:
            missed_covered.append(rid)
        if not r:
            missed_related.append(rid)
        if x:
            contradictions.append(rid)

    # grounded_precision: of the items the answer actually characterised, the
    # fraction it characterised truthfully (did not contradict). None when the
    # answer covered nothing — there is no claim to be right or wrong about.
    grounded_precision = round(1 - contradicted / covered, 4) if covered else None

    return {
        "total": n,
        "covered": covered,
        "related": related,
        "contradicted": contradicted,
        "covered_recall": round(covered / n, 4) if n else 0.0,
        "related_recall": round(related / n, 4) if n else 0.0,
        "grounded_precision": grounded_precision,
        "missed_covered": missed_covered,
        "missed_related": missed_related,
        "contradictions": contradictions,
        "items": items,
    }


def _passes(votes, n, rule):
    """Did `votes` of `n` judges suffice under `rule`?

    unanimous — all judges agree (the high-precision gate, default for the noisy
                contradiction axis).
    majority  — strictly more than half (for n=2 this equals unanimous; it only
                differs at n>=3).
    any       — at least one judge (the union; lowest precision).
    """
    if n <= 0:
        return False
    if rule == "unanimous":
        return votes == n
    if rule == "majority":
        return votes * 2 > n
    if rule == "any":
        return votes > 0
    raise ValueError(f"unknown agreement rule: {rule}")


def reconcile_audits(audits, labels=None, *, covered_rule="majority",
                     contradicted_rule="unanimous"):
    """Combine N per-judge audits into one consensus audit (the agreement gate).

    The contradiction call is the subjective, noisy axis: two independent judge
    models rarely make the SAME idiosyncratic error on the SAME item, so an
    item only counts as `contradicted` when ENOUGH judges agree (default:
    unanimous). This trades recall for precision on fabrication flags — exactly
    right for a "smoking gun" you must not raise falsely. Recall (covered) stays
    robust across judges, so it uses a gentler `majority` rule.

    `audits`  — list of dicts returned by grade() (None entries are dropped).
    `labels`  — optional per-judge names (e.g. model ids) for the vote detail.

    Returns a consensus audit shaped like grade()'s output, plus:
      `judges`               — labels of the audits that were combined.
      `dropped_contradictions` — items some-but-not-enough judges flagged
                                 contradicted (the noise the gate removed),
                                 each with its vote count — so a suppressed
                                 flag is logged, never silently dropped.
      per-item `votes`       — {covered, related, contradicted} tallies.

    Returns None if fewer than one non-None audit is supplied.
    """
    audits = [a for a in audits if a]
    if not audits:
        return None
    n = len(audits)
    labels = list(labels or [f"judge{i+1}" for i in range(n)])[:n]

    # Union of item ids, first-seen order across the audits.
    ids, seen = [], set()
    for a in audits:
        for it in a.get("items", []) or []:
            rid = it.get("id")
            if rid is not None and rid not in seen:
                seen.add(rid)
                ids.append(rid)

    per_judge = [{it.get("id"): it for it in (a.get("items") or [])} for a in audits]

    items, covered, related, contradicted = [], 0, 0, 0
    contradictions, missed_covered, missed_related, dropped = [], [], [], []
    for rid in ids:
        cv = sum(1 for j in per_judge if j.get(rid, {}).get("covered"))
        rv = sum(1 for j in per_judge if j.get(rid, {}).get("related"))
        xv = sum(1 for j in per_judge if j.get(rid, {}).get("contradicted"))

        c = _passes(cv, n, covered_rule)
        x = c and _passes(xv, n, contradicted_rule)
        r = c and (not x) and _passes(rv, n, covered_rule)
        items.append({"id": rid, "covered": c, "related": r, "contradicted": x,
                      "votes": {"covered": cv, "related": rv, "contradicted": xv}})
        covered += 1 if c else 0
        related += 1 if r else 0
        contradicted += 1 if x else 0
        if not c:
            missed_covered.append(rid)
        if not r:
            missed_related.append(rid)
        if x:
            contradictions.append(rid)
        elif xv > 0:
            # Flagged by some judges but not enough to pass the gate.
            who = [labels[i] for i, j in enumerate(per_judge) if j.get(rid, {}).get("contradicted")]
            dropped.append({"id": rid, "votes": xv, "of": n, "by": who})

    total = len(ids)
    grounded_precision = round(1 - contradicted / covered, 4) if covered else None
    return {
        "total": total,
        "covered": covered,
        "related": related,
        "contradicted": contradicted,
        "covered_recall": round(covered / total, 4) if total else 0.0,
        "related_recall": round(related / total, 4) if total else 0.0,
        "grounded_precision": grounded_precision,
        "missed_covered": missed_covered,
        "missed_related": missed_related,
        "contradictions": contradictions,
        "dropped_contradictions": dropped,
        "judges": labels,
        "agreement": {"covered_rule": covered_rule, "contradicted_rule": contradicted_rule, "n": n},
        "items": items,
    }
