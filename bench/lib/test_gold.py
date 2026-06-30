#!/usr/bin/env python3
"""Unit tests for gold.py — gold-target recall scoring.

Run from anywhere:
    python3 -m unittest bench.lib.test_gold

Or directly:
    python3 bench/lib/test_gold.py

The cited-recall snippets are lifted verbatim from real baseline answers in
results/baseline/{rails,solidus}/ — the formats a real Opus 4.6 run actually
used to pin a line. The point of these tests is that every one of those forms
earns cited credit, so baseline stops being under-credited and the
"sense-only cited" artifact disappears.
"""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import gold  # noqa: E402
from scorer import read_transcript_texts  # noqa: E402


class IsFileLikeTest(unittest.TestCase):
    def test_code_extension(self):
        self.assertTrue(gold._is_file_like("anthropic.rb"))
        self.assertTrue(gold._is_file_like("queue_adapters/async_adapter.rb"))

    def test_path_with_dot(self):
        self.assertTrue(gold._is_file_like("a/b.conf"))

    def test_pure_symbol_is_not_file_like(self):
        self.assertFalse(gold._is_file_like("TopicCreator"))
        self.assertFalse(gold._is_file_like("parse_error"))


class CitedFormsTest(unittest.TestCase):
    """Each accepted location-pin form, lower-cased like the real caller."""

    def test_path_colon_line(self):
        # Form 1 — full gold path immediately followed by a line (rails used
        # this for one adapter via "file_line").
        hay = '"file_line": "activejob/lib/active_job/queue_adapters/abstract_adapter.rb:9-23",'.lower()
        self.assertTrue(gold._cited("queue_adapters/abstract_adapter.rb", hay))

    def test_path_paren_line(self):
        # Form 2 — trailing "(line N)" parenthetical.
        hay = "see providers/anthropic.rb (line 24) for the mapping".lower()
        self.assertTrue(gold._cited("providers/anthropic.rb", hay))

    def test_path_then_json_line_field(self):
        # Form 3 — JSON sibling "line" field (solidus used this for the
        # *_level_condition concerns).
        hay = (
            '{"name": "OrderLevelCondition", '
            '"file": "promotions/app/models/concerns/solidus_promotions/conditions/order_level_condition.rb", '
            '"line": 5, "note": "Deprecated"}'
        ).lower()
        self.assertTrue(gold._cited("conditions/order_level_condition.rb", hay))

    def test_json_line_field_does_not_cross_object_boundary(self):
        # The path's own object has no line; a *different* object right after
        # carries one. The } between them must block the match.
        hay = (
            '{"file": "promotions/app/models/solidus_promotions/order_adjuster.rb"}, '
            '{"other": "x", "line": 5}'
        ).lower()
        self.assertFalse(gold._cited("solidus_promotions/order_adjuster.rb", hay))

    def test_json_line_field_window_is_bounded(self):
        # A "line" field far past the path (beyond the window) is not its pin.
        far = "x" * 200
        hay = f'"file": "lib/foo.rb", "{far}": 1, "line": 5'.lower()
        self.assertFalse(gold._cited("lib/foo.rb", hay))

    def test_unique_basename_line(self):
        # Form 4 — basename + line when the full path was named but the line
        # pinned via the basename (solidus: "reads at benefit.rb:260").
        hay = (
            '"benefit_base_class": "promotions/app/models/solidus_promotions/benefit.rb"\n'
            '"note": "what benefit#available_calculators reads at benefit.rb:260."'
        ).lower()
        self.assertTrue(gold._cited("solidus_promotions/benefit.rb", hay, basename="benefit.rb"))

    def test_basename_disabled_when_not_passed(self):
        # Same haystack, no basename allowed -> not cited (the full path has no
        # adjacent line of its own).
        hay = '"file": "promotions/app/models/solidus_promotions/benefit.rb"\nreads at benefit.rb:260.'.lower()
        self.assertFalse(gold._cited("solidus_promotions/benefit.rb", hay))

    def test_basename_guard_rejects_longer_sibling_path(self):
        # basename "product.rb" must NOT match inside "line_item_product.rb:6".
        hay = '"file": "conditions/line_item_product.rb:6"'.lower()
        self.assertFalse(gold._cited("conditions/product.rb", hay, basename="product.rb"))

    def test_full_path_named_but_never_pinned_is_not_cited(self):
        hay = '"file": "promotions/app/models/solidus_promotions/order_adjuster.rb",'.lower()
        self.assertFalse(gold._cited("solidus_promotions/order_adjuster.rb", hay, basename="order_adjuster.rb"))


class GoldBasenamesTest(unittest.TestCase):
    def test_unique_basenames_kept(self):
        g = [
            {"match": ["queue_adapters/async_adapter.rb"]},
            {"match": ["queue_adapters/inline_adapter.rb"]},
        ]
        m = gold._gold_basenames(g)
        self.assertEqual(m["queue_adapters/async_adapter.rb"], "async_adapter.rb")
        self.assertEqual(m["queue_adapters/inline_adapter.rb"], "inline_adapter.rb")

    def test_shared_basename_dropped(self):
        # Two distinct gold targets ending in the same basename -> neither may
        # use the short-basename cited form.
        g = [
            {"match": ["a/foo.rb"]},
            {"match": ["b/foo.rb"]},
        ]
        m = gold._gold_basenames(g)
        self.assertNotIn("a/foo.rb", m)
        self.assertNotIn("b/foo.rb", m)

    def test_string_shorthand_and_symbol_ignored(self):
        g = ["TopicCreator", {"match": ["lib/x.rb"]}]
        m = gold._gold_basenames(g)
        self.assertEqual(m, {"lib/x.rb": "x.rb"})


class ScoreGoldRecallTest(unittest.TestCase):
    def test_none_gold_returns_none(self):
        self.assertIsNone(gold.score_gold_recall("anything", None))
        self.assertIsNone(gold.score_gold_recall("anything", []))

    def test_pure_symbol_cited_equals_mentioned(self):
        r = gold.score_gold_recall("we call TopicCreator here", ["TopicCreator"])
        self.assertEqual(r["mention_recall"], 1.0)
        self.assertEqual(r["cited_recall"], 1.0)

    def test_symbol_missing_is_zero(self):
        r = gold.score_gold_recall("nothing here", ["TopicCreator"])
        self.assertEqual(r["mention_recall"], 0.0)
        self.assertEqual(r["cited_recall"], 0.0)
        self.assertEqual(r["missed_mention"], ["TopicCreator"])
        self.assertEqual(r["missed_cite"], ["TopicCreator"])

    def test_mentioned_but_not_cited(self):
        g = [{"id": "adapter", "group": "g", "match": ["queue_adapters/async_adapter.rb"]}]
        r = gold.score_gold_recall('"file": "lib/queue_adapters/async_adapter.rb",', g)
        self.assertEqual(r["mention_recall"], 1.0)
        self.assertEqual(r["cited_recall"], 0.0)
        self.assertEqual(r["missed_cite"], ["adapter"])

    def test_groups_breakdown(self):
        g = [
            {"id": "a", "group": "adapters", "match": ["x/async_adapter.rb"]},
            {"id": "b", "group": "adapters", "match": ["x/inline_adapter.rb"]},
        ]
        text = '"x/async_adapter.rb:1" and "x/inline_adapter.rb"'
        r = gold.score_gold_recall(text, g)
        grp = r["groups"]["adapters"]
        self.assertEqual(grp["total"], 2)
        self.assertEqual(grp["mention_recall"], 1.0)
        self.assertEqual(grp["cited_recall"], 0.5)

    def test_cited_implies_mentioned_via_basename(self):
        # Regression: an answer that pins a target only by basename+line
        # ("async_adapter.rb:50") without the full gold path must count as BOTH
        # cited and mentioned — cited_recall can never exceed mention_recall.
        g = [{"id": "async", "group": "adapters",
              "match": ["queue_adapters/async_adapter.rb"]}]
        r = gold.score_gold_recall("see async_adapter.rb:50 for enqueue", g)
        self.assertTrue(r["details"][0]["cited"])
        self.assertTrue(r["details"][0]["mentioned"])
        self.assertEqual(r["mention_recall"], 1.0)
        self.assertEqual(r["cited_recall"], 1.0)

    def test_cited_never_exceeds_mention_across_set(self):
        g = [
            {"id": "a", "group": "g", "match": ["queue_adapters/async_adapter.rb"]},
            {"id": "b", "group": "g", "match": ["queue_adapters/inline_adapter.rb"]},
            {"id": "c", "group": "g", "match": ["queue_adapters/resque_adapter.rb"]},
        ]
        # basenames+lines for a,b; nothing for c
        r = gold.score_gold_recall("async_adapter.rb:1 inline_adapter.rb:2", g)
        self.assertLessEqual(r["cited_recall"], r["mention_recall"])
        self.assertEqual(r["cited"], 2)
        self.assertEqual(r["mentioned"], 2)

    def test_shared_basename_targets_do_not_double_count(self):
        # Two gold targets share basename foo.rb; the answer pins only a/foo.rb.
        # b/foo.rb must NOT also score cited off the shared basename.
        g = [
            {"id": "a", "group": "g", "match": ["a/foo.rb"]},
            {"id": "b", "group": "g", "match": ["b/foo.rb"]},
        ]
        r = gold.score_gold_recall('"a/foo.rb:12" named; b/foo.rb has no line', g)
        self.assertEqual(r["cited"], 1)
        self.assertEqual(r["missed_cite"], ["b"])


class GoldUniqueSuffixTest(unittest.TestCase):
    """Multi-segment gold-unique suffix: credit a repo-relative citation whose
    basename collides between two gold targets (so form-4 is disabled), while
    excluding a cross-tree sibling that shares the same tail."""

    SALEOR = [
        {"id": "order-build", "group": "dependents",
         "match": ["saleor/order/utils.py"]},
        {"id": "checkout-append", "group": "dependents",
         "match": ["saleor/checkout/utils.py"]},
        {"id": "bulk", "group": "dependents",
         "match": ["saleor/graphql/order/bulk_mutations/order_bulk_create.py"]},
    ]

    def test_unique_suffixes_skip_colliding_basename(self):
        allp = ["saleor/order/utils.py", "saleor/checkout/utils.py"]
        sufs = gold._gold_unique_suffixes("saleor/order/utils.py", allp)
        self.assertIn("order/utils.py", sufs)        # 2-seg suffix is gold-unique
        self.assertNotIn("utils.py", sufs)           # bare basename collides → out

    def _det(self, text):
        r = gold.score_gold_recall(text, self.SALEOR)
        return {x["id"]: x for x in r["details"]}

    def test_repo_relative_citation_credited(self):
        # Agent working INSIDE the saleor/ package writes the natural
        # repo-relative path (no saleor/ prefix). utils.py collides → form-4 off;
        # the 2-segment gold-unique suffix order/utils.py must still credit.
        d = self._det("repriced at order/utils.py:248")
        self.assertTrue(d["order-build"]["cited"])
        self.assertTrue(d["order-build"]["mentioned"])
        self.assertFalse(d["checkout-append"]["cited"])

    def test_cross_tree_sibling_not_credited(self):
        # graphql/order/utils.py is a DIFFERENT real file; citing it must NOT
        # credit the saleor/order/utils.py gold (the '/' before order is blocked).
        d = self._det("see graphql/order/utils.py:248")
        self.assertFalse(d["order-build"]["cited"])

    def test_full_prefixed_path_still_credited(self):
        d = self._det("saleor/checkout/utils.py:334 reads base price")
        self.assertTrue(d["checkout-append"]["cited"])

    def test_bare_colliding_basename_not_credited(self):
        # bare utils.py:5 is ambiguous between two gold targets → neither cited.
        d = self._det("utils.py:5")
        self.assertFalse(d["order-build"]["cited"])
        self.assertFalse(d["checkout-append"]["cited"])

    def test_suffix_paren_and_json_line_forms(self):
        d = self._det("order/utils.py (line 248)")
        self.assertTrue(d["order-build"]["cited"])
        d = self._det('{"file": "checkout/utils.py", "line": 334}')
        self.assertTrue(d["checkout-append"]["cited"])

    def test_named_without_line_is_mention_not_cite(self):
        d = self._det("the reprice lives in order/utils.py somewhere")
        self.assertTrue(d["order-build"]["mentioned"])
        self.assertFalse(d["order-build"]["cited"])


class ScoreGoldF1Test(unittest.TestCase):
    """Precision/recall/F1 of the claimed (cited) file set vs gold."""

    GOLD = [
        {"id": "model", "group": "contract", "match": ["models/reaction.rb"]},
        {"id": "handler", "group": "write", "match": ["reaction_handler.rb"]},
        {"id": "worker", "group": "cascade",
         "match": ["reactions/update_relevant_scores_worker.rb"]},
        {"id": "score", "group": "scoring", "match": ["update_score"]},  # symbol-only
    ]

    def test_none_gold_returns_none(self):
        self.assertIsNone(gold.score_gold_f1(["a.rb"], None))
        self.assertIsNone(gold.score_gold_f1(["a.rb"], []))

    def test_symbol_only_gold_returns_none(self):
        # No file-like targets → nothing to score precision over.
        self.assertIsNone(gold.score_gold_f1(["a.rb"], ["TopicCreator", "update_score"]))

    def test_clean_set_is_full_precision(self):
        # Every cited file is on the impact set → precision 1.0; all 3 file
        # targets hit → recall 1.0.
        claimed = [
            "app/models/reaction.rb",
            "app/services/reaction_handler.rb",
            "app/workers/reactions/update_relevant_scores_worker.rb",
        ]
        r = gold.score_gold_f1(claimed, self.GOLD)
        self.assertEqual(r["gold_files"], 3)  # symbol-only "score" excluded
        self.assertEqual(r["precision"], 1.0)
        self.assertEqual(r["recall"], 1.0)
        self.assertEqual(r["f1"], 1.0)

    def test_noise_costs_precision_not_recall(self):
        # The grep-noise mechanism: same 3 real targets, plus 3 off-target
        # files. Recall stays 1.0; precision drops to 3/6.
        claimed = [
            "app/models/reaction.rb",
            "app/services/reaction_handler.rb",
            "app/workers/reactions/update_relevant_scores_worker.rb",
            "config/routes.rb",
            "app/policies/reaction_policy.rb",
            "app/javascript/packs/articleReactions.js",
        ]
        r = gold.score_gold_f1(claimed, self.GOLD)
        self.assertEqual(r["recall"], 1.0)
        self.assertEqual(r["precision"], 0.5)
        self.assertEqual(r["false_positives"], 3)
        self.assertAlmostEqual(r["f1"], 2 * 0.5 * 1.0 / 1.5, places=4)

    def test_missed_target_costs_recall(self):
        claimed = ["app/models/reaction.rb"]
        r = gold.score_gold_f1(claimed, self.GOLD)
        self.assertEqual(r["precision"], 1.0)
        self.assertAlmostEqual(r["recall"], 1 / 3, places=4)
        self.assertIn("worker", r["missed"])
        self.assertIn("handler", r["missed"])

    def test_abbreviated_citation_credited_via_unique_basename(self):
        # Agent cited just the basename; the gold pattern is path-qualified.
        # Unique basename → still a true positive, not a false positive.
        r = gold.score_gold_f1(["update_relevant_scores_worker.rb"], self.GOLD)
        self.assertEqual(r["true_positives"], 1)
        self.assertEqual(r["false_positives"], 0)
        self.assertEqual(r["precision"], 1.0)

    def test_dedup_distinct_files(self):
        r = gold.score_gold_f1(
            ["app/models/reaction.rb", "app/models/reaction.rb"], self.GOLD)
        self.assertEqual(r["claimed"], 1)

    def test_empty_claim_is_zero(self):
        r = gold.score_gold_f1([], self.GOLD)
        self.assertEqual(r["precision"], 0.0)
        self.assertEqual(r["recall"], 0.0)
        self.assertEqual(r["f1"], 0.0)


class RealBaselineSnippetTest(unittest.TestCase):
    """End-to-end on the actual baseline answers the fix was diagnosed from.

    Skips cleanly when results/ is absent (clean checkout / CI).
    """

    def _answer(self, repo):
        path = os.path.join(
            os.path.dirname(os.path.abspath(__file__)),
            "..", "results", "baseline", repo, "transcript.json",
        )
        if not os.path.exists(path):
            self.skipTest(f"results/baseline/{repo} not present")
        answer, _ = read_transcript_texts(path)
        return answer

    def test_rails_adapters_now_cited(self):
        # Form 1+4: rails baseline named all 8 adapter paths and pinned several
        # via basename (async_adapter.rb:64-67). Old matcher scored these 0.
        answer = self._answer("rails")
        g = [
            {"id": "async", "group": "adapters", "match": ["queue_adapters/async_adapter.rb"]},
            {"id": "abstract", "group": "adapters", "match": ["queue_adapters/abstract_adapter.rb"]},
        ]
        r = gold.score_gold_recall(answer, g)
        self.assertEqual(r["mention_recall"], 1.0)
        self.assertEqual(r["cited_recall"], 1.0, r["missed_cite"])

    def test_solidus_benefit_now_cited(self):
        # Form 4: solidus baseline named the full benefit.rb path and pinned the
        # line as "benefit.rb:260".
        answer = self._answer("solidus")
        g = [{"id": "benefit", "group": "registry", "match": ["solidus_promotions/benefit.rb"]}]
        r = gold.score_gold_recall(answer, g)
        self.assertTrue(r["details"][0]["mentioned"])
        self.assertTrue(r["details"][0]["cited"], "benefit.rb:260 should now count")


if __name__ == "__main__":
    unittest.main()
