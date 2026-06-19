#!/usr/bin/env python3
"""Unit tests for reporter.py — the split-axes headline rendering.

Run directly:
    python3 bench/lib/test_reporter.py

Covers the headline table (mention / fixed-cited / billed-context) and the
billed-context delta, plus the secondary fairness table still rendering.
"""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import reporter  # noqa: E402


class FmtRecallTest(unittest.TestCase):
    def test_none_is_dash(self):
        self.assertEqual(reporter._fmt_recall(None, None, None), "—")

    def test_with_counts(self):
        self.assertEqual(reporter._fmt_recall(0.7778, 14, 18), "78% (14/18)")

    def test_recall_only_when_counts_missing(self):
        self.assertEqual(reporter._fmt_recall(0.5, None, None), "50%")
        self.assertEqual(reporter._fmt_recall(0.5, 3, 0), "50%")


class FmtBilledDeltaTest(unittest.TestCase):
    def test_sense_loads_less(self):
        rows = [
            {"tool": "baseline", "billed": 28226},
            {"tool": "sense", "billed": 18671},
        ]
        self.assertAlmostEqual(reporter._fmt_billed_delta(rows), -0.3385, places=3)

    def test_sense_loads_more(self):
        rows = [
            {"tool": "baseline", "billed": 9474},
            {"tool": "sense", "billed": 12393},
        ]
        self.assertGreater(reporter._fmt_billed_delta(rows), 0)

    def test_missing_arm_returns_none(self):
        self.assertIsNone(reporter._fmt_billed_delta([{"tool": "sense", "billed": 5}]))

    def test_zero_baseline_returns_none(self):
        rows = [{"tool": "baseline", "billed": 0}, {"tool": "sense", "billed": 5}]
        self.assertIsNone(reporter._fmt_billed_delta(rows))


class HeadlineTableTest(unittest.TestCase):
    def _rows(self):
        return [
            {"tool": "baseline", "mention_recall": 0.92, "cited_recall": 0.92,
             "gold_mentioned": 11, "gold_cited": 11, "gold_total": 12,
             "billed": 28226, "uncached_in": 8140},
            {"tool": "sense", "mention_recall": 1.0, "cited_recall": 0.833,
             "gold_mentioned": 12, "gold_cited": 10, "gold_total": 12,
             "billed": 18671, "uncached_in": 23},
        ]

    def test_renders_both_arms_and_delta(self):
        md = "\n".join(reporter._headline_table_md(self._rows()))
        self.assertIn("Mention recall", md)
        self.assertIn("Cited recall (fixed)", md)
        self.assertIn("92% (11/12)", md)   # baseline mention
        self.assertIn("83% (10/12)", md)   # sense cited
        self.assertIn("28,226", md)        # billed
        self.assertIn("-34%", md)          # billed delta
        self.assertIn("Sense loads less", md)

    def test_alphabetical_order_baseline_first(self):
        rows = list(reversed(self._rows()))  # feed sense-first
        out = reporter._headline_table_md(rows)
        body = [l for l in out if l.startswith("| ") and "Tool" not in l and "---" not in l]
        self.assertTrue(body[0].startswith("| baseline"))
        self.assertTrue(body[1].startswith("| sense"))

    def test_failed_row_and_no_gold(self):
        rows = [
            {"tool": "baseline", "failed": True},
            {"tool": "sense", "mention_recall": None, "cited_recall": None,
             "billed": None, "uncached_in": None},
        ]
        md = "\n".join(reporter._headline_table_md(rows))
        self.assertIn("**FAILED**", md)
        self.assertIn("| sense | — | — | — | — |", md)
        # No delta line when an arm is missing usable billed tokens.
        self.assertNotIn("Billed-context Δ", md)


class FormatMarkdownSmokeTest(unittest.TestCase):
    """End-to-end render of one repo so format_markdown's new branches run."""

    def test_primary_and_secondary_tables_present(self):
        tables = [{
            "repo": "lobsters",
            "rows": [
                {"tool": "baseline", "fairness_score": 0.767, "fairness_complete": True,
                 "adoption_score": 0.4, "keyword_coverage": 1.0, "llm_quality": 0.85,
                 "efficiency": 0.5, "tokens": 28226, "billed": 28226, "uncached_in": 8140,
                 "mention_recall": 0.92, "cited_recall": 0.92, "gold_mentioned": 11,
                 "gold_cited": 11, "gold_total": 12, "wall_time": 120.0, "cost_usd": 1.0,
                 "cites_grounded": 5, "cites_total": 5, "cites_hallucinated": 0},
                {"tool": "sense", "fairness_score": 0.774, "fairness_complete": True,
                 "adoption_score": 0.7, "keyword_coverage": 1.0, "llm_quality": 0.86,
                 "efficiency": 0.6, "tokens": 18671, "billed": 18671, "uncached_in": 23,
                 "mention_recall": 1.0, "cited_recall": 0.833, "gold_mentioned": 12,
                 "gold_cited": 10, "gold_total": 12, "wall_time": 100.0, "cost_usd": 0.9,
                 "cites_grounded": 4, "cites_total": 4, "cites_hallucinated": 0},
            ],
        }]
        aggregate = [{
            "tool": "sense", "scenarios": 1, "failures": 0, "avg_fairness": 0.774,
            "avg_adoption": 0.7, "avg_keyword_coverage": 1.0, "avg_llm_quality": 0.86,
            "avg_efficiency": 0.6, "avg_tokens": 18671, "avg_time": 100.0,
            "total_cost": 0.9, "cites_grounded": 4, "cites_total": 4,
            "cites_hallucinated": 0, "avg_grounding": 1.0,
        }]
        md = reporter.format_markdown(tables, aggregate, {"total": 1})
        self.assertIn("Cited recall (fixed)", md)            # primary table
        self.assertIn("Secondary — locked fairness", md)      # secondary table
        self.assertIn("0.774", md)                            # fairness preserved
        self.assertIn("-34%", md)                             # billed delta


if __name__ == "__main__":
    unittest.main()


class RankByRecallHeadline(unittest.TestCase):
    """Judging Contract rule 1: the aggregate ranks by objective cited-recall +
    relationship_audit, NEVER by the omission-blind fairness/llm_quality. This
    guards against the regression where the blind composite (which rated the
    chatwoot +0.44 win as a tie) drives the headline ranking."""

    def test_ranks_by_recall_not_blind_fairness(self):
        # baseline: high blind fairness, low recall. sense: low fairness, high recall.
        # The blind composite would rank baseline first; recall must rank sense first.
        results = [
            {"tool": "baseline", "fairness_score": 0.90, "llm_quality": 0.90,
             "gold_recall": {"cited_recall": 0.40}, "relationship_audit": 0.40, "metrics": {}},
            {"tool": "sense", "fairness_score": 0.50, "llm_quality": 0.50,
             "gold_recall": {"cited_recall": 0.90}, "relationship_audit": 0.95, "metrics": {}},
        ]
        agg = reporter.build_aggregate(results)
        self.assertEqual(agg[0]["tool"], "sense",
                         "ranking must follow cited_recall, not the blind fairness composite")
        self.assertAlmostEqual(agg[0]["avg_cited_recall"], 0.90)
        self.assertAlmostEqual(agg[0]["avg_relationship_audit"], 0.95)

    def test_headline_columns_present_in_markdown(self):
        results = [
            {"tool": "sense", "fairness_score": 0.5, "llm_quality": 0.5,
             "gold_recall": {"cited_recall": 0.9}, "relationship_audit": 0.9, "metrics": {}},
        ]
        agg = reporter.build_aggregate(results)
        md = reporter.format_markdown([], agg, {"total": 1})
        self.assertIn("Cited Recall", md)
        self.assertIn("Rel Audit", md)
