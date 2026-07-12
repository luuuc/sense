#!/usr/bin/env python3
"""Unit tests for check_article_stats.py — the article freshness gate.

Run directly:
    python3 bench/lib/test_check_article_stats.py

Builds a tiny fixture (one model root with baseline/sense scored.json +
judged.json + a draft article) and asserts FRESH when the claimed headline and
axes blocks match the recomputed numbers, OUTDATED when either drifts (incl. a
tampered axes.cited_delta), and NO DATA when the bench root is empty.
"""

import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import check_article_stats as cas


def _scored(cited, total, overall):
    return {"gold_recall": {"cited_recall": overall,
                            "groups": {"dependents": {"cited": cited, "total": total}}}}


def _judged(related, covered, contradicted):
    return {"judge": {"model": "test-judge"},
            "relationship_audit": {"related_recall": related,
                                   "covered": covered,
                                   "contradicted": contradicted}}


def _write_run(root, model, arm, repo, payload, judged=None):
    d = os.path.join(root, "results", model, arm, repo)
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "scored.json"), "w") as f:
        json.dump(payload, f)
    if judged is not None:
        with open(os.path.join(d, "judged.json"), "w") as f:
            json.dump(judged, f)


def _write_article(adir, name, repo, deps_delta, ofrom, oto, data="results/m1",
                   axes=None, headline=True):
    os.makedirs(adir, exist_ok=True)
    fm = f"---\nrepo: {repo}\noutcome: \"x\"\n"
    if headline:
        fm += (f"headline: {{repo: {repo}, deps_delta: {deps_delta}, "
               f"overall_from: {ofrom}, overall_to: {oto}}}\n")
    if axes:
        fm += f"axes: {axes}\n"
    fm += f"data: {data}\n---\n# body\n"
    with open(os.path.join(adir, name), "w") as f:
        f.write(fm)


# A truthful axes: claim for the foo fixture below. Live values:
#   cited_delta 0.90-0.40=0.50; deps_delta 0.70
#   b_score b: .55*0.40+.25*0.80+.20*0.90=0.60; s: .55*0.90+.25*0.90+.20*1.0=0.92
#   related 0.80->0.90; grounded 0.90/1.00 (1 of 10 contradicted); contra 1/0
# judge/runs/eff_at_parity_win are metadata and must be ignored by the check.
AXES_FOO = ("{cited_delta: 0.50, deps_delta: 0.70, "
            "b_score_from: 0.60, b_score_to: 0.92, "
            "related_from: 0.80, related_to: 0.90, "
            "grounded_b: 0.90, grounded_s: 1.00, contra_b: 1, contra_s: 0, "
            "judge: \"test-judge, RUNS=2\", runs: 2, eff_at_parity_win: true}")


class CheckArticleStats(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.adir = os.path.join(self.root, "articles")
        # baseline 2/10 deps, 0.40 overall; sense 9/10 deps, 0.90 overall.
        _write_run(self.root, "m1", "baseline", "foo", _scored(2, 10, 0.40),
                   judged=_judged(0.80, covered=10, contradicted=1))
        _write_run(self.root, "m1", "sense", "foo", _scored(9, 10, 0.90),
                   judged=_judged(0.90, covered=10, contradicted=0))

    def _status(self, name):
        rows = cas.check(self.adir, root=self.root)
        return dict((n, s) for n, s, _ in rows)[name]

    def test_fresh_when_claim_matches(self):
        # live deps delta = 0.9 - 0.2 = 0.70; overall 0.40 -> 0.90.
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.40, 0.90)
        self.assertEqual(self._status("foo.md"), "FRESH")

    def test_outdated_when_deps_drifts(self):
        _write_article(self.adir, "foo.md", "foo", 0.56, 0.40, 0.90)
        self.assertTrue(self._status("foo.md").startswith("OUTDATED"))

    def test_outdated_when_overall_drifts(self):
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.39, 0.98)
        self.assertTrue(self._status("foo.md").startswith("OUTDATED"))

    def test_no_data_when_root_empty(self):
        _write_article(self.adir, "bar.md", "bar", 0.5, 0.1, 0.6)  # no runs for 'bar'
        self.assertEqual(self._status("bar.md"), "NO DATA")

    def test_articles_without_headline_skipped(self):
        os.makedirs(self.adir, exist_ok=True)
        with open(os.path.join(self.adir, "essay.md"), "w") as f:
            f.write("---\nrepo: (all)\ndata: results/\n---\n# essay\n")
        names = [n for n, _, _ in cas.check(self.adir, root=self.root)]
        self.assertNotIn("essay.md", names)

    def test_missing_deps_group_skips_deps_not_flagged(self):
        # A repo whose gold has no dependents group: deps not comparable, overall matches → FRESH.
        _write_run(self.root, "m1", "baseline", "tie", {"gold_recall": {"cited_recall": 1.0, "groups": {}}})
        _write_run(self.root, "m1", "sense", "tie", {"gold_recall": {"cited_recall": 1.0, "groups": {}}})
        _write_article(self.adir, "tie.md", "tie", 0.0, 1.0, 1.0)
        self.assertEqual(self._status("tie.md"), "FRESH")

    def test_fresh_when_axes_match(self):
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.40, 0.90, axes=AXES_FOO)
        self.assertEqual(self._status("foo.md"), "FRESH")

    def test_outdated_when_axes_cited_delta_tampered(self):
        # The 2026-07-11 tamper: falsify cited_delta, keep everything else true.
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.40, 0.90,
                       axes=AXES_FOO.replace("cited_delta: 0.50", "cited_delta: 0.30"))
        status = self._status("foo.md")
        self.assertTrue(status.startswith("OUTDATED"))
        self.assertIn("axes.cited_delta +0.30->+0.50", status)

    def test_outdated_when_axes_judge_score_drifts(self):
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.40, 0.90,
                       axes=AXES_FOO.replace("related_to: 0.90", "related_to: 0.95"))
        status = self._status("foo.md")
        self.assertTrue(status.startswith("OUTDATED"))
        self.assertIn("axes.related_to", status)

    def test_axes_alias_shape_related_b_s(self):
        # Rails-era shape: related_b/related_s instead of related_from/related_to.
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.40, 0.90,
                       axes="{cited_delta: 0.50, related_b: 0.80, related_s: 0.90}")
        self.assertEqual(self._status("foo.md"), "FRESH")

    def test_axes_single_grounded_flags_arm_drift(self):
        # `grounded: 1.00` claims both arms; live baseline grounded is 0.90.
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.40, 0.90,
                       axes="{cited_delta: 0.50, grounded: 1.00}")
        status = self._status("foo.md")
        self.assertTrue(status.startswith("OUTDATED"))
        self.assertIn("axes.grounded_b 1.00->0.90", status)

    def test_axes_only_article_checked(self):
        # Gem-feeder shape: no headline: block, top-level repo + axes + data.
        _write_article(self.adir, "gem.md", "foo", None, None, None,
                       axes=AXES_FOO, headline=False)
        self.assertEqual(self._status("gem.md"), "FRESH")

    def test_axes_only_article_tamper_caught(self):
        _write_article(self.adir, "gem.md", "foo", None, None, None,
                       axes=AXES_FOO.replace("contra_b: 1", "contra_b: 0"),
                       headline=False)
        status = self._status("gem.md")
        self.assertTrue(status.startswith("OUTDATED"))
        self.assertIn("axes.contra_b 0->1", status)

    def test_frontmatter_parses_inline_headline(self):
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.40, 0.90)
        fm = cas.frontmatter(os.path.join(self.adir, "foo.md"))
        self.assertEqual(fm["headline"]["repo"], "foo")
        self.assertEqual(fm["data"], "results/m1")


if __name__ == "__main__":
    unittest.main()
