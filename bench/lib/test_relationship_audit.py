#!/usr/bin/env python3
"""Unit tests for relationship_audit.py — reference-aware relationship grading.

The judge call is stubbed, so these run offline and deterministically. They
pin the scoring math and the omission/relation semantics, not the LLM.
"""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import relationship_audit as ra  # noqa: E402


GOLD = [
    {"id": "model", "group": "contract", "match": ["models/inbox.rb"],
     "relation": "the model itself"},
    {"id": "concern", "group": "contract", "match": ["concerns/channelable.rb"],
     "relation": "has_one :inbox, as: :channel"},
    {"id": "dep", "group": "dependents", "match": ["whatsapp/channel_creation_service.rb"],
     "relation": "creates the inbox for a channel"},
    {"id": "no-rel", "group": "dependents", "match": ["lib/x.rb"]},  # no relation → skipped
]


def _stub(items):
    """Return (call_judge, extract_json) that always yield the given items."""
    payload = {"items": items}
    return (lambda system, user, *, api_key=None: {"_": payload}), (lambda resp: resp["_"])


class BuildReferenceTest(unittest.TestCase):
    def test_only_targets_with_relation(self):
        ref = ra.build_reference(GOLD)
        self.assertEqual([r[0] for r in ref], ["model", "concern", "dep"])
        self.assertEqual(ref[0][1], "models/inbox.rb")  # where = first match
        self.assertEqual(ref[1][2], "has_one :inbox, as: :channel")

    def test_string_shorthand_skipped(self):
        self.assertEqual(ra.build_reference(["bare", {"match": ["a.rb"]}]), [])

    def test_user_prompt_includes_relations_and_answer(self):
        ref = ra.build_reference(GOLD)
        p = ra.build_user_prompt("MY ANSWER", ref, "the Inbox contract")
        self.assertIn("the Inbox contract", p)
        self.assertIn("true_relation: creates the inbox for a channel", p)
        self.assertIn("MY ANSWER", p)


class GradeTest(unittest.TestCase):
    def test_none_when_no_relations(self):
        cj, ex = _stub([])
        self.assertIsNone(ra.grade("a", [{"match": ["x.rb"]}], call_judge=cj, extract_json=ex))

    def test_all_covered_and_related(self):
        cj, ex = _stub([
            {"id": "model", "covered": True, "related": True},
            {"id": "concern", "covered": True, "related": True},
            {"id": "dep", "covered": True, "related": True},
        ])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertEqual(r["total"], 3)
        self.assertEqual(r["covered_recall"], 1.0)
        self.assertEqual(r["related_recall"], 1.0)

    def test_omission_lowers_covered_recall(self):
        # The fix: an item the answer never names is covered=false, even if the
        # grader were tempted to infer it from the reference.
        cj, ex = _stub([
            {"id": "model", "covered": True, "related": True},
            {"id": "concern", "covered": False, "related": False},
            {"id": "dep", "covered": False, "related": False},
        ])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertAlmostEqual(r["covered_recall"], 1 / 3, places=4)
        self.assertEqual(set(r["missed_covered"]), {"concern", "dep"})

    def test_related_requires_covered(self):
        # related is forced false when covered is false, regardless of the
        # grader's `related` value (you cannot relate what you never named).
        cj, ex = _stub([
            {"id": "model", "covered": True, "related": True},
            {"id": "concern", "covered": True, "related": False},  # named, no relation
            {"id": "dep", "covered": False, "related": True},      # contradictory → 0
        ])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertEqual(r["covered"], 2)
        self.assertEqual(r["related"], 1)  # only "model"
        self.assertIn("dep", r["missed_covered"])
        self.assertIn("concern", r["missed_related"])

    def test_string_bools_coerced(self):
        cj, ex = _stub([
            {"id": "model", "covered": "true", "related": "true"},
            {"id": "concern", "covered": "false", "related": "false"},
            {"id": "dep", "covered": "yes", "related": "no"},
        ])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertEqual(r["covered"], 2)   # model + dep
        self.assertEqual(r["related"], 1)   # model only

    def test_missing_item_counts_as_uncovered(self):
        # Grader omits an item entirely → treated as covered=false.
        cj, ex = _stub([{"id": "model", "covered": True, "related": True}])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertEqual(r["covered"], 1)
        self.assertEqual(set(r["missed_covered"]), {"concern", "dep"})


class ContradictionTest(unittest.TestCase):
    """Fix 3 — anti-fabrication: a covered item with a confident-FALSE relation is
    `contradicted`, strictly worse than omission, and drops grounded_precision."""

    def test_contradiction_penalises_precision_not_recall(self):
        # All three named; one (dep) is characterised with a WRONG relation.
        cj, ex = _stub([
            {"id": "model", "covered": True, "related": True, "contradicted": False},
            {"id": "concern", "covered": True, "related": True, "contradicted": False},
            {"id": "dep", "covered": True, "related": False, "contradicted": True},
        ])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertEqual(r["covered"], 3)          # all named → recall unchanged
        self.assertEqual(r["covered_recall"], 1.0)
        self.assertEqual(r["contradicted"], 1)
        self.assertEqual(r["contradictions"], ["dep"])
        self.assertEqual(r["related"], 2)          # the contradicted item is NOT related
        self.assertAlmostEqual(r["grounded_precision"], 2 / 3, places=4)

    def test_contradiction_beats_related_when_both_set(self):
        # A grader that marks both related and contradicted: contradiction wins,
        # so a false claim can never be laundered into a related credit.
        cj, ex = _stub([
            {"id": "model", "covered": True, "related": True, "contradicted": True},
            {"id": "concern", "covered": True, "related": True, "contradicted": False},
            {"id": "dep", "covered": False, "related": False, "contradicted": True},
        ])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertEqual(r["contradicted"], 1)     # only "model" (dep not covered)
        self.assertEqual(r["contradictions"], ["model"])
        self.assertEqual(r["related"], 1)          # only "concern"

    def test_no_contradictions_gives_full_precision(self):
        cj, ex = _stub([
            {"id": "model", "covered": True, "related": True},
            {"id": "concern", "covered": True, "related": False},   # vague, not false
            {"id": "dep", "covered": False, "related": False},
        ])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertEqual(r["contradicted"], 0)
        self.assertEqual(r["grounded_precision"], 1.0)   # silence/vagueness ≠ fabrication

    def test_precision_none_when_nothing_covered(self):
        cj, ex = _stub([
            {"id": "model", "covered": False},
            {"id": "concern", "covered": False},
            {"id": "dep", "covered": False},
        ])
        r = ra.grade("a", GOLD, call_judge=cj, extract_json=ex)
        self.assertIsNone(r["grounded_precision"])


class ReconcileAuditsTest(unittest.TestCase):
    """2-judge-agreement gate: a contradiction counts only when judges agree
    (unanimous by default); recall uses a gentler majority rule."""

    def _audit(self, items):
        # Minimal audit dict (only the fields reconcile_audits reads).
        return {"items": items}

    def test_contradiction_requires_unanimity(self):
        # liquid-embed flagged by BOTH → kept; trend-detector by ONE → dropped.
        a1 = self._audit([
            {"id": "liquid", "covered": True, "related": False, "contradicted": True},
            {"id": "trend", "covered": True, "related": False, "contradicted": True},
            {"id": "ok", "covered": True, "related": True, "contradicted": False},
        ])
        a2 = self._audit([
            {"id": "liquid", "covered": True, "related": False, "contradicted": True},
            {"id": "trend", "covered": True, "related": True, "contradicted": False},
            {"id": "ok", "covered": True, "related": True, "contradicted": False},
        ])
        r = ra.reconcile_audits([a1, a2], labels=["opus", "sonnet"])
        self.assertEqual(r["contradictions"], ["liquid"])         # only the agreed one
        self.assertEqual(r["contradicted"], 1)
        self.assertEqual([d["id"] for d in r["dropped_contradictions"]], ["trend"])
        self.assertEqual(r["dropped_contradictions"][0]["by"], ["opus"])  # who flagged it
        self.assertAlmostEqual(r["grounded_precision"], 2 / 3, places=4)  # 1 of 3 covered is contra

    def test_recall_uses_majority_not_unanimity(self):
        # For n=2, majority == both; an item only one judge covered is not covered.
        a1 = self._audit([{"id": "x", "covered": True, "related": True, "contradicted": False}])
        a2 = self._audit([{"id": "x", "covered": False, "related": False, "contradicted": False}])
        r = ra.reconcile_audits([a1, a2])
        self.assertEqual(r["covered"], 0)
        self.assertEqual(r["total"], 1)

    def test_three_judge_majority_keeps_two_of_three_recall(self):
        mk = lambda cov: {"items": [{"id": "x", "covered": cov, "related": cov, "contradicted": False}]}
        r = ra.reconcile_audits([mk(True), mk(True), mk(False)])
        self.assertEqual(r["covered"], 1)   # 2/3 majority covers it
        # but a contradiction needs all three
        x = lambda c: {"items": [{"id": "y", "covered": True, "related": False, "contradicted": c}]}
        r2 = ra.reconcile_audits([x(True), x(True), x(False)])
        self.assertEqual(r2["contradicted"], 0)
        self.assertEqual(r2["dropped_contradictions"][0]["votes"], 2)

    def test_none_audits_dropped(self):
        a = self._audit([{"id": "x", "covered": True, "related": True, "contradicted": False}])
        self.assertIsNone(ra.reconcile_audits([None, None]))
        r = ra.reconcile_audits([a, None])
        self.assertEqual(r["covered"], 1)   # single surviving audit, unanimous over n=1


if __name__ == "__main__":
    unittest.main()
