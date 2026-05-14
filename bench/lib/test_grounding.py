#!/usr/bin/env python3
"""Unit tests for grounding.py.

Run from anywhere:
    python3 -m unittest bench.lib.test_grounding

Or directly:
    python3 bench/lib/test_grounding.py
"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import grounding  # noqa: E402
from scorer import read_transcript_texts  # noqa: E402


class ExtractCitationsTest(unittest.TestCase):
    def test_line_citation(self):
        text = "Look at `lib/topic_creator.rb:117` for the call."
        self.assertEqual(
            grounding.extract_citations(text),
            [("lib/topic_creator.rb", "117")],
        )

    def test_symbol_citation(self):
        text = "See `base-server.ts:handleRequest` for the path."
        self.assertEqual(
            grounding.extract_citations(text),
            [("base-server.ts", "handleRequest")],
        )

    def test_class_method_citation(self):
        text = "Triggered in `lib/post_creator.rb:PostCreator#create`."
        self.assertEqual(
            grounding.extract_citations(text),
            [("lib/post_creator.rb", "PostCreator#create")],
        )

    def test_deduplicates(self):
        text = (
            "First at `lib/topic_creator.rb:117`, "
            "and again at `lib/topic_creator.rb:117` later."
        )
        self.assertEqual(
            grounding.extract_citations(text),
            [("lib/topic_creator.rb", "117")],
        )

    def test_excludes_non_source_extensions(self):
        # _SOURCE_FILE_RE is allowlist-based — .md doesn't match by design.
        text = "Doc at `README.md:42` should be skipped, but `app.py:42` shouldn't."
        self.assertEqual(
            grounding.extract_citations(text),
            [("app.py", "42")],
        )


class ClassifyLocatorTest(unittest.TestCase):
    def test_pure_digits_is_line(self):
        self.assertEqual(grounding._classify_locator("117"), (117, None))

    def test_identifier_is_symbol(self):
        self.assertEqual(grounding._classify_locator("Foo"), (None, "Foo"))

    def test_class_method_returns_trailing_identifier(self):
        self.assertEqual(grounding._classify_locator("Class#method"), (None, "method"))
        self.assertEqual(grounding._classify_locator("Class.method"), (None, "method"))

    def test_trailing_line_number(self):
        self.assertEqual(grounding._classify_locator("Foo:42"), (42, "Foo"))


class GroundCitationTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = self.tmp.name
        os.makedirs(os.path.join(self.repo, "lib"), exist_ok=True)
        # 20-line file with TopicCreator defined on line 7.
        with open(os.path.join(self.repo, "lib", "topic_creator.rb"), "w") as f:
            f.write(
                "# header\n"
                "module Discourse\n"
                "  # comment\n"
                "  # comment\n"
                "  # comment\n"
                "  # comment\n"
                "  class TopicCreator\n"
                "    def initialize(user, guardian, opts)\n"
                "      @user = user\n"
                "    end\n"
                "    def create\n"
                "      ensure_can_create!\n"
                "    end\n"
                "    def ensure_can_create!\n"
                "      raise unless @guardian.can_create?\n"
                "    end\n"
                "  end\n"
                "end\n"
                "# trailer\n"
                "# trailer\n"
            )

    def tearDown(self):
        self.tmp.cleanup()

    def test_missing_file_is_unresolved(self):
        r = grounding.ground_citation("lib/missing.rb", "10", self.repo)
        self.assertEqual(r["status"], "unresolved")
        self.assertIn("not found", r["reason"])

    def test_line_in_range_is_grounded(self):
        r = grounding.ground_citation("lib/topic_creator.rb", "7", self.repo)
        self.assertEqual(r["status"], "grounded")

    def test_line_out_of_range_is_hallucinated(self):
        r = grounding.ground_citation("lib/topic_creator.rb", "5000", self.repo)
        self.assertEqual(r["status"], "hallucinated")
        self.assertIn("only 20 lines", r["reason"])

    def test_symbol_found_file_wide(self):
        r = grounding.ground_citation(
            "lib/topic_creator.rb", "TopicCreator", self.repo
        )
        self.assertEqual(r["status"], "grounded")

    def test_symbol_not_found_anywhere(self):
        r = grounding.ground_citation(
            "lib/topic_creator.rb", "NotARealSymbol", self.repo
        )
        self.assertEqual(r["status"], "unresolved")

    def test_symbol_near_cited_line(self):
        # ensure_can_create! is on line 14; cite line 12, within ±5.
        r = grounding.ground_citation(
            "lib/topic_creator.rb", "ensure_can_create:12", self.repo
        )
        self.assertEqual(r["status"], "grounded")

    def test_symbol_far_from_cited_line(self):
        # ensure_can_create! is on line 14; cite line 2.
        r = grounding.ground_citation(
            "lib/topic_creator.rb", "ensure_can_create:2", self.repo
        )
        self.assertEqual(r["status"], "unresolved")
        self.assertIn("±5", r["reason"])

    def test_class_method_uses_trailing_identifier(self):
        # `create` is on line 11; cite TopicCreator#create:11 — should
        # grep for `create`, find it within window.
        r = grounding.ground_citation(
            "lib/topic_creator.rb", "TopicCreator#create:11", self.repo
        )
        self.assertEqual(r["status"], "grounded")

    def test_word_boundary_does_not_match_substring(self):
        # `create` should NOT match `topic_creator` (no word boundary).
        # Place a symbol that's only present as a substring.
        r = grounding.ground_citation(
            "lib/topic_creator.rb", "topic_creato", self.repo
        )
        self.assertEqual(r["status"], "unresolved")


class GroundCitationsAggregateTest(unittest.TestCase):
    def test_skipped_when_no_repo(self):
        text = "Look at `app.py:42` for details."
        r = grounding.ground_citations(text, None)
        self.assertEqual(r["total"], 1)
        self.assertEqual(r["grounded"], 0)
        self.assertIn("skipped", r)

    def test_mixed_results(self):
        with tempfile.TemporaryDirectory() as tmp:
            with open(os.path.join(tmp, "real.py"), "w") as f:
                f.write("def hello():\n    pass\n")
            text = (
                "Real: `real.py:1`. "
                "Beyond EOF: `real.py:5000`. "
                "Missing file: `missing.py:1`."
            )
            r = grounding.ground_citations(text, tmp)
            self.assertEqual(r["total"], 3)
            self.assertEqual(r["grounded"], 1)
            self.assertEqual(r["hallucinated"], 1)
            self.assertEqual(r["unresolved"], 1)
            self.assertAlmostEqual(r["rate"], 1 / 3, places=3)


class RealTranscriptSmokeTest(unittest.TestCase):
    """The pitch asks for unit-testing against 5 sample transcripts.

    We can't ship the transcripts in tests (they're hundreds of KB), but
    we *can* sanity-check that extraction runs cleanly against every
    transcript currently in results/ and produces at least some
    citations for the rich ones. If results/ is absent (clean checkout
    on CI), the test is skipped.
    """

    def test_extracts_from_existing_transcripts(self):
        results = os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "..", "results"
        )
        if not os.path.isdir(results):
            self.skipTest("bench/results/ not present")

        found_any = False
        for tool in os.listdir(results):
            tool_dir = os.path.join(results, tool)
            if not os.path.isdir(tool_dir):
                continue
            for repo in os.listdir(tool_dir):
                t = os.path.join(tool_dir, repo, "transcript.json")
                if not os.path.exists(t):
                    continue
                answer, _ = read_transcript_texts(t)
                cites = grounding.extract_citations(answer)
                # Just assert the call doesn't blow up and returns a list.
                self.assertIsInstance(cites, list)
                if cites:
                    found_any = True

        self.assertTrue(found_any, "no citations extracted from any transcript")


if __name__ == "__main__":
    unittest.main()
