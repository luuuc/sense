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


if __name__ == "__main__":
    unittest.main()
