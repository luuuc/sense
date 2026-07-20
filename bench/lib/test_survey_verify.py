#!/usr/bin/env python3
"""Unit tests for survey_verify.py: post-run agent survey parse + verification.

Run from anywhere:
    python3 -m unittest bench.lib.test_survey_verify

Or directly:
    python3 bench/lib/test_survey_verify.py

The point of these tests is the killer step: a cited instance only earns
verified=true when transcript.json actually contains the call (and, for q2/q3,
the fallback-after / changed-query-after ordering). Everything else is stamped
confabulated, kept, and counted.
"""

import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import survey_verify  # noqa: E402


def tool_use(name, inp):
    return {"type": "assistant", "message": {"content": [
        {"type": "tool_use", "name": name, "input": inp}]}}


def tool_result(text):
    return {"type": "user", "message": {"content": [
        {"type": "tool_result", "content": [{"type": "text", "text": text}]}]}}


class TestParseAnswer(unittest.TestCase):
    def test_bare_json(self):
        ans = survey_verify.parse_answer('{"score": 8, "q4_value": "blast"}')
        self.assertEqual(ans["score"], 8)

    def test_fenced_json_with_prose(self):
        text = 'Here is my feedback:\n```json\n{"score": 6, "q5_improve": "shorter"}\n```\nthanks'
        ans = survey_verify.parse_answer(text)
        self.assertEqual(ans["score"], 6)

    def test_garbage_returns_none(self):
        self.assertIsNone(survey_verify.parse_answer("I loved it, ten out of ten"))
        self.assertIsNone(survey_verify.parse_answer(""))


class TestFinalText(unittest.TestCase):
    def test_prefers_result_event(self):
        rows = [
            {"type": "assistant", "message": {"content": [{"type": "text", "text": "thinking..."}]}},
            {"type": "result", "result": '{"score": 9}'},
        ]
        self.assertEqual(survey_verify._final_text(rows), '{"score": 9}')

    def test_falls_back_to_last_assistant_text(self):
        rows = [
            {"type": "assistant", "message": {"content": [{"type": "text", "text": "first"}]}},
            {"type": "assistant", "message": {"content": [{"type": "text", "text": '{"score": 5}'}]}},
        ]
        self.assertEqual(survey_verify._final_text(rows), '{"score": 5}')


class TestVerify(unittest.TestCase):
    def events(self, rows):
        return survey_verify._events(rows)

    def test_q1_verified_when_call_exists(self):
        events = self.events([tool_use("mcp__sense__sense_graph", {"symbol": "AbstractAlbum"})])
        answer = {"q1_accurate": [{"tool": "sense_graph", "query": "AbstractAlbum", "note": "hub"}],
                  "score": 8}
        counts = survey_verify.verify(answer, events)
        self.assertEqual(counts, {"instances": 1, "verified": 1, "confabulated": 0})
        self.assertTrue(answer["q1_accurate"][0]["verified"])

    def test_q1_confabulated_when_query_never_ran(self):
        events = self.events([tool_use("mcp__sense__sense_graph", {"symbol": "User"})])
        answer = {"q1_wrong": [{"tool": "sense_graph", "query": "PaymentGateway", "note": "empty"}]}
        counts = survey_verify.verify(answer, events)
        self.assertEqual(counts["confabulated"], 1)
        self.assertFalse(answer["q1_wrong"][0]["verified"])

    def test_q2_needs_fallback_after_the_call(self):
        with_fallback = self.events([
            tool_use("mcp__sense__sense_blast", {"symbol": "Album"}),
            tool_use("Grep", {"pattern": "Album"}),
        ])
        without = self.events([
            tool_use("Grep", {"pattern": "Album"}),
            tool_use("mcp__sense__sense_blast", {"symbol": "Album"}),
        ])
        inst = {"tool": "sense_blast", "query": "Album", "fallback": "grep", "missing": "callers"}
        self.assertEqual(survey_verify.verify({"q2_fallbacks": [dict(inst)]}, with_fallback)["verified"], 1)
        self.assertEqual(survey_verify.verify({"q2_fallbacks": [dict(inst)]}, without)["verified"], 0)

    def test_q2_bash_grep_counts_as_fallback(self):
        events = self.events([
            tool_use("mcp__sense__sense_search", {"query": "album routing"}),
            tool_use("Bash", {"command": "grep -rn Album app/"}),
        ])
        answer = {"q2_fallbacks": [{"tool": "sense_search", "query": "album routing",
                                    "fallback": "grep", "missing": "file"}]}
        self.assertEqual(survey_verify.verify(answer, events)["verified"], 1)

    def test_q3_requires_before_then_after_ordering(self):
        events = self.events([
            tool_use("mcp__sense__sense_graph", {"symbol": "save"}),
            tool_result("ambiguous symbol; pass file to disambiguate"),
            tool_use("mcp__sense__sense_graph", {"symbol": "save", "file": "app/models/album.rb"}),
        ])
        answer = {"q3_hints": [{"tool": "sense_graph", "query_before": '"symbol": "save"}',
                               "query_after": "app/models/album.rb",
                               "hint": "ambiguous symbol; pass file to disambiguate"}]}
        counts = survey_verify.verify(answer, events)
        self.assertEqual(counts["verified"], 1)
        self.assertTrue(answer["q3_hints"][0]["hint_found"])

    def test_empty_answer_zero_instances(self):
        counts = survey_verify.verify({"score": 5}, [])
        self.assertEqual(counts, {"instances": 0, "verified": 0, "confabulated": 0})


class TestBuildRecord(unittest.TestCase):
    def test_end_to_end_record(self):
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "transcript.json"), "w") as fh:
                fh.write(json.dumps(tool_use("mcp__sense__sense_blast", {"symbol": "Album"})) + "\n")
                fh.write(json.dumps(tool_use("Read", {"file_path": "app/models/album.rb"})) + "\n")
            survey = {"type": "result", "result": json.dumps({
                "q1_accurate": [{"tool": "sense_blast", "query": "Album", "note": "impact"}],
                "q1_wrong": [], "q2_fallbacks": [], "q3_hints": [],
                "q4_value": "transitive impact", "q5_improve": "shorter output",
                "score": 8, "score_rationale": "fewer reads"})}
            with open(os.path.join(d, "survey.json"), "w") as fh:
                fh.write(json.dumps(survey) + "\n")
            with open(os.path.join(d, "run_meta.json"), "w") as fh:
                json.dump({"repo": "lychee", "scenario": "album-hub", "model": "claude-opus-4-8",
                           "tool_version": "sense 1.12.3", "repo_commit": "abc1234"}, fh)
            rec, err = survey_verify.build_record(d, {"vertical": "php-laravel"})
            self.assertIsNone(err)
            self.assertEqual(rec["score"], 8)
            self.assertEqual(rec["repo"], "lychee")
            self.assertEqual(rec["vertical"], "php-laravel")
            self.assertEqual(rec["verify"], {"instances": 1, "verified": 1, "confabulated": 0})

    def test_missing_survey_is_skip(self):
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            rec, err = survey_verify.build_record(d)
            self.assertIsNone(rec)
            self.assertIn("survey.json", err)


if __name__ == "__main__":
    unittest.main()
