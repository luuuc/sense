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

    def test_primary_table_and_blind_composite_retired(self):
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
        self.assertIn("Cited recall (fixed)", md)            # primary table present
        self.assertIn("-34%", md)                            # billed delta present
        # The blind fairness composite is RETIRED — no secondary table, and the
        # llm_quality/fairness composite must not be rendered as a scoreboard.
        self.assertNotIn("Secondary — locked fairness", md)
        self.assertNotIn("LLM Quality", md)
        self.assertIn("RETIRED", md)
        self.assertIn("B-score", md)                          # the fair replacement


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


class FabricationAggregateTest(unittest.TestCase):
    """Fix 3 — the aggregate surfaces grounded_precision + the contradiction count
    so a confident-false baseline is visibly penalised, not rewarded."""

    def test_aggregates_precision_and_contradictions(self):
        results = [
            {"tool": "baseline", "gold_recall": {"cited_recall": 0.8},
             "relationship_audit": 0.8, "grounded_precision": 0.50,
             "contradictions": 3, "metrics": {}},
            {"tool": "sense", "gold_recall": {"cited_recall": 0.9},
             "relationship_audit": 0.9, "grounded_precision": 1.0,
             "contradictions": 0, "metrics": {}},
        ]
        agg = reporter.build_aggregate(results)
        by = {r["tool"]: r for r in agg}
        self.assertAlmostEqual(by["baseline"]["avg_grounded_precision"], 0.50)
        self.assertEqual(by["baseline"]["contradictions"], 3)
        self.assertAlmostEqual(by["sense"]["avg_grounded_precision"], 1.0)
        self.assertEqual(by["sense"]["contradictions"], 0)
        md = reporter.format_markdown([], agg, {"total": 2})
        self.assertIn("Grounded Prec.", md)
        self.assertIn("Contradict.", md)

    def test_missing_precision_renders_none(self):
        # Pre-Fix-3 data (no grounded_precision/contradictions) stays unchanged.
        results = [
            {"tool": "sense", "gold_recall": {"cited_recall": 0.9},
             "relationship_audit": 0.9, "metrics": {}},
        ]
        agg = reporter.build_aggregate(results)
        self.assertIsNone(agg[0]["avg_grounded_precision"])
        self.assertEqual(agg[0]["contradictions"], 0)


class BScoreTest(unittest.TestCase):
    """The cited-dominant fair composite that replaced the blind fairness score."""

    def test_b_score_formula(self):
        results = [
            {"tool": "sense", "gold_recall": {"cited_recall": 0.80},
             "relationship_audit": 0.90, "related_recall": 0.70,
             "grounded_precision": 1.0, "metrics": {}},
        ]
        agg = reporter.build_aggregate(results)
        # 0.55*0.80 + 0.25*0.70 + 0.20*1.0 = 0.44 + 0.175 + 0.20 = 0.815
        self.assertAlmostEqual(agg[0]["b_score"], 0.815, places=4)

    def test_b_score_none_without_relation_axes(self):
        # Gems (no relation gold) → related/grounded_precision absent → B None.
        results = [
            {"tool": "sense", "gold_recall": {"cited_recall": 0.80},
             "relationship_audit": None, "metrics": {}},
        ]
        agg = reporter.build_aggregate(results)
        self.assertIsNone(agg[0]["b_score"])


class ProcessEfficiencyHeldRecallTest(unittest.TestCase):
    """Fix 4 / Judging Contract rule 5: process-cost savings are surfaced ONLY at
    held recall — never as a standalone rank over a less-complete answer."""

    def _agg(self, base_recall, sense_recall):
        results = [
            {"tool": "baseline", "gold_recall": {"cited_recall": base_recall},
             "relationship_audit": base_recall,
             "metrics": {"read_count": 16, "tool_calls": 20, "token_total_billed": 100000}},
            {"tool": "sense", "gold_recall": {"cited_recall": sense_recall},
             "relationship_audit": sense_recall,
             "metrics": {"read_count": 4, "tool_calls": 6, "token_total_billed": 65000}},
        ]
        return reporter.build_aggregate(results)

    def test_savings_shown_at_parity(self):
        md = "\n".join(reporter._process_efficiency_md(self._agg(0.90, 0.90)))
        self.assertIn("Process efficiency (at held recall)", md)
        self.assertIn("parity", md)
        self.assertIn("Reads", md)
        self.assertIn("-75%", md)   # 16 → 4 reads

    def test_savings_shown_as_bonus_when_recall_higher(self):
        md = "\n".join(reporter._process_efficiency_md(self._agg(0.70, 0.95)))
        self.assertIn("HIGHER", md)
        self.assertIn("Reads", md)

    def test_not_claimed_when_recall_below_parity(self):
        md = "\n".join(reporter._process_efficiency_md(self._agg(0.90, 0.60)))
        self.assertIn("NOT claimed", md)
        # The savings table must be withheld when recall is below parity.
        self.assertNotIn("| Reads |", md)

    def test_single_arm_skips_block(self):
        results = [{"tool": "sense", "gold_recall": {"cited_recall": 0.9},
                    "relationship_audit": 0.9, "metrics": {}}]
        self.assertEqual(reporter._process_efficiency_md(reporter.build_aggregate(results)), [])
