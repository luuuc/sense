#!/usr/bin/env python3
"""Unit tests for check_article_stats.py — the article freshness gate.

Run directly:
    python3 bench/lib/test_check_article_stats.py

Builds a tiny fixture (one model root with baseline/sense scored.json + a draft
article) and asserts FRESH when the claimed headline matches the recomputed
numbers, OUTDATED when it drifts, and NO DATA when the bench root is empty.
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


def _write_run(root, model, arm, repo, payload):
    d = os.path.join(root, "results", model, arm, repo)
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "scored.json"), "w") as f:
        json.dump(payload, f)


def _write_article(adir, name, repo, deps_delta, ofrom, oto, data="results/m1"):
    os.makedirs(adir, exist_ok=True)
    fm = (f"---\nrepo: {repo}\noutcome: \"x\"\n"
          f"headline: {{repo: {repo}, deps_delta: {deps_delta}, "
          f"overall_from: {ofrom}, overall_to: {oto}}}\n"
          f"data: {data}\n---\n# body\n")
    with open(os.path.join(adir, name), "w") as f:
        f.write(fm)


class CheckArticleStats(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.adir = os.path.join(self.root, "articles")
        # baseline 2/10 deps, 0.40 overall; sense 9/10 deps, 0.90 overall.
        _write_run(self.root, "m1", "baseline", "foo", _scored(2, 10, 0.40))
        _write_run(self.root, "m1", "sense", "foo", _scored(9, 10, 0.90))

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

    def test_frontmatter_parses_inline_headline(self):
        _write_article(self.adir, "foo.md", "foo", 0.70, 0.40, 0.90)
        fm = cas.frontmatter(os.path.join(self.adir, "foo.md"))
        self.assertEqual(fm["headline"]["repo"], "foo")
        self.assertEqual(fm["data"], "results/m1")


if __name__ == "__main__":
    unittest.main()
