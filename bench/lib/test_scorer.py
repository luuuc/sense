#!/usr/bin/env python3
"""Tests for scorer.py — pure functions only, no file I/O."""

import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(__file__))

from scorer import (
    classify_tool_calls,
    detect_misses,
    extract_json_from_text,
    normalize_caller,
    parse_transcript,
    score_keyword_presence,
    score_set_match,
)


class TestNormalizeCaller(unittest.TestCase):
    def test_file_symbol(self):
        self.assertEqual(normalize_caller("foo.go:Bar"), "foo.go:Bar")

    def test_file_line_symbol(self):
        self.assertEqual(normalize_caller("foo.go:42 Bar"), "foo.go:Bar")

    def test_file_line_colon_symbol(self):
        self.assertEqual(normalize_caller("foo.go:42:Bar"), "foo.go:Bar")

    def test_strips_whitespace(self):
        self.assertEqual(normalize_caller("  foo.go:42 Bar  "), "foo.go:Bar")

    def test_plain_string(self):
        self.assertEqual(normalize_caller("something"), "something")

    def test_multi_word_takes_last(self):
        self.assertEqual(
            normalize_caller("internal/blast/engine.go:15 TestComputeDirect"),
            "internal/blast/engine.go:TestComputeDirect",
        )

    def test_strips_go_package_prefix(self):
        self.assertEqual(
            normalize_caller("internal/benchmark/benchmark.go:104 benchmark.benchBlast"),
            "internal/benchmark/benchmark.go:benchBlast",
        )

    def test_strips_go_test_package_prefix(self):
        self.assertEqual(
            normalize_caller("internal/blast/blast_bench_test.go:69 blast_test.runBlastBench"),
            "internal/blast/blast_bench_test.go:runBlastBench",
        )

    def test_keeps_type_method_prefix(self):
        self.assertEqual(
            normalize_caller("internal/mcpserver/server.go:handlers.blastSymbol"),
            "internal/mcpserver/server.go:handlers.blastSymbol",
        )

    def test_strips_root_level_go_package(self):
        self.assertEqual(
            normalize_caller("context.go:42 gin.Context"),
            "context.go:Context",
        )

    def test_root_level_go_type_method(self):
        self.assertEqual(
            normalize_caller("gin.go:42 gin.Engine.ServeHTTP"),
            "gin.go:Engine.ServeHTTP",
        )

    def test_strips_trailing_paren(self):
        self.assertEqual(
            normalize_caller("context.go:42 gin.Context.JSON)"),
            "context.go:Context.JSON",
        )

    def test_ruby_file_unchanged(self):
        self.assertEqual(
            normalize_caller("app/models/post.rb:Post"),
            "app/models/post.rb:Post",
        )

    def test_ruby_class_method(self):
        self.assertEqual(
            normalize_caller("lib/post_creator.rb:PostCreator#create_topic"),
            "lib/post_creator.rb:PostCreator#create_topic",
        )

    def test_bare_symbol_strips_go_package(self):
        self.assertEqual(normalize_caller("gin.Engine.ServeHTTP"), "Engine.ServeHTTP")

    def test_bare_symbol_strips_package_for_type(self):
        self.assertEqual(normalize_caller("gin.HandlerFunc"), "HandlerFunc")

    def test_bare_symbol_keeps_type_method(self):
        self.assertEqual(normalize_caller("node.getValue"), "node.getValue")

    def test_bare_symbol_keeps_scan_lowercase(self):
        self.assertEqual(normalize_caller("scan.indexedFile"), "scan.indexedFile")


class TestScoreSetMatch(unittest.TestCase):
    def test_perfect_match(self):
        response = {"callers": ["a.go:Foo", "b.go:Bar"]}
        gt = {"callers": ["a.go:Foo", "b.go:Bar"]}
        result = score_set_match(response, gt, "callers")
        self.assertEqual(result["f1"], 1.0)
        self.assertEqual(result["precision"], 1.0)
        self.assertEqual(result["recall"], 1.0)

    def test_partial_match(self):
        response = {"callers": ["a.go:Foo", "b.go:Bar"]}
        gt = {"callers": ["a.go:Foo", "b.go:Bar", "c.go:Baz"]}
        result = score_set_match(response, gt, "callers")
        self.assertAlmostEqual(result["precision"], 1.0)
        self.assertAlmostEqual(result["recall"], 2 / 3, places=4)
        self.assertGreater(result["f1"], 0)
        self.assertLess(result["f1"], 1.0)

    def test_no_match(self):
        response = {"callers": ["x.go:Nope"]}
        gt = {"callers": ["a.go:Foo"]}
        result = score_set_match(response, gt, "callers")
        self.assertEqual(result["f1"], 0.0)

    def test_empty_response(self):
        result = score_set_match({"callers": []}, {"callers": ["a.go:Foo"]}, "callers")
        self.assertEqual(result["f1"], 0.0)
        self.assertEqual(result["found_count"], 0)

    def test_none_response(self):
        result = score_set_match(None, {"callers": ["a.go:Foo"]}, "callers")
        self.assertEqual(result["f1"], 0.0)

    def test_normalizes_file_line_symbol(self):
        response = {"callers": ["a.go:10 Foo"]}
        gt = {"callers": ["a.go:Foo"]}
        result = score_set_match(response, gt, "callers")
        self.assertEqual(result["f1"], 1.0)

    def test_reports_fp_fn(self):
        response = {"callers": ["a.go:Foo", "x.go:Extra"]}
        gt = {"callers": ["a.go:Foo", "b.go:Missing"]}
        result = score_set_match(response, gt, "callers")
        self.assertEqual(result["true_positives"], ["a.go:Foo"])
        self.assertEqual(result["false_positives"], ["x.go:Extra"])
        self.assertEqual(result["false_negatives"], ["b.go:Missing"])


class TestScoreKeywordPresence(unittest.TestCase):
    def test_all_found(self):
        text = "This has tree-sitter parsing and SQLite storage"
        gt = {"keywords": ["tree-sitter", "SQLite"]}
        result = score_keyword_presence(text, gt, "keywords")
        self.assertEqual(result["score"], 1.0)

    def test_partial(self):
        text = "This mentions tree-sitter but not the database"
        gt = {"keywords": ["tree-sitter", "SQLite"]}
        result = score_keyword_presence(text, gt, "keywords")
        self.assertEqual(result["score"], 0.5)

    def test_none_found(self):
        text = "Nothing relevant here"
        gt = {"keywords": ["tree-sitter", "SQLite"]}
        result = score_keyword_presence(text, gt, "keywords")
        self.assertEqual(result["score"], 0.0)

    def test_case_insensitive(self):
        text = "TREE-SITTER and sqlite"
        gt = {"keywords": ["tree-sitter", "SQLite"]}
        result = score_keyword_presence(text, gt, "keywords")
        self.assertEqual(result["score"], 1.0)

    def test_empty_keywords(self):
        result = score_keyword_presence("anything", {"keywords": []}, "keywords")
        self.assertEqual(result["score"], 0.0)


class TestExtractJsonFromText(unittest.TestCase):
    def test_plain_json(self):
        self.assertEqual(
            extract_json_from_text('{"callers": ["a"]}'), {"callers": ["a"]}
        )

    def test_fenced_json(self):
        text = 'Here are the results:\n```json\n{"callers": ["a"]}\n```\nDone.'
        self.assertEqual(extract_json_from_text(text), {"callers": ["a"]})

    def test_fenced_no_lang(self):
        text = 'Results:\n```\n{"callers": ["a"]}\n```'
        self.assertEqual(extract_json_from_text(text), {"callers": ["a"]})

    def test_not_json(self):
        self.assertIsNone(extract_json_from_text("just some text"))

    def test_empty(self):
        self.assertIsNone(extract_json_from_text(""))


class TestClassifyToolCalls(unittest.TestCase):
    def test_mixed(self):
        calls = [
            {"name": "mcp__sense__sense_graph", "input": {}},
            {"name": "mcp__sense__sense_search", "input": {}},
            {"name": "Read", "input": {}},
            {"name": "Bash", "input": {}},
        ]
        result = classify_tool_calls(calls)
        self.assertEqual(result["total"], 4)
        self.assertEqual(result["mcp_calls"], 2)
        self.assertEqual(result["builtin_calls"], 2)

    def test_empty(self):
        result = classify_tool_calls([])
        self.assertEqual(result["total"], 0)


class TestDetectMisses(unittest.TestCase):
    def test_baseline_has_no_misses(self):
        calls = [{"name": "Bash", "input": {"command": "grep -r foo ."}}]
        result = detect_misses(calls, "baseline")
        self.assertEqual(result["total"], 0)

    def test_grep_is_miss_for_search_tool(self):
        calls = [{"name": "Bash", "input": {"command": "grep -r pattern ."}}]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 1)
        self.assertEqual(result["misses"][0]["type"], "search_miss")
        self.assertEqual(result["misses"][0]["classification"], "miss")

    def test_grep_after_mcp_search_is_verification(self):
        calls = [
            {"name": "mcp__sense__sense_search", "input": {"query": "foo"}},
            {"name": "Bash", "input": {"command": "grep -r foo ."}},
        ]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 0)
        self.assertEqual(len(result["verifications"]), 1)
        self.assertEqual(result["verifications"][0]["classification"], "verification")

    def test_grep_tool_is_miss(self):
        calls = [{"name": "Grep", "input": {"pattern": "foo"}}]
        result = detect_misses(calls, "grepai")
        self.assertEqual(result["total"], 1)
        self.assertEqual(result["misses"][0]["type"], "search_miss")

    def test_glob_is_miss(self):
        calls = [{"name": "Glob", "input": {"pattern": "**/*.go"}}]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 1)
        self.assertEqual(result["misses"][0]["type"], "search_miss")

    def test_agent_miss(self):
        calls = [{"name": "Agent", "input": {"description": "explore codebase"}}]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 1)
        self.assertEqual(result["misses"][0]["type"], "agent_miss")

    def test_agent_after_mcp_is_verification(self):
        calls = [
            {"name": "mcp__sense__sense_graph", "input": {}},
            {"name": "Agent", "input": {"description": "explore"}},
        ]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 0)
        self.assertEqual(len(result["verifications"]), 1)

    def test_many_reads_is_graph_miss(self):
        calls = [{"name": "Read", "input": {}} for _ in range(6)]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 1)
        self.assertEqual(result["misses"][0]["type"], "graph_miss")

    def test_many_reads_with_graph_call_is_verification(self):
        calls = [
            {"name": "mcp__sense__sense_graph", "input": {}},
        ] + [{"name": "Read", "input": {}} for _ in range(6)]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 0)
        self.assertEqual(len(result["verifications"]), 1)

    def test_few_reads_not_flagged(self):
        calls = [{"name": "Read", "input": {}} for _ in range(4)]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 0)

    def test_unknown_tool(self):
        calls = [{"name": "Bash", "input": {"command": "grep foo"}}]
        result = detect_misses(calls, "unknown_tool")
        self.assertEqual(result["total"], 0)
        self.assertTrue(result.get("unconfigured"))

    def test_mcp_search_far_away_is_still_miss(self):
        calls = [
            {"name": "mcp__sense__sense_search", "input": {"query": "foo"}},
            {"name": "Read", "input": {}},
            {"name": "Read", "input": {}},
            {"name": "Read", "input": {}},
            {"name": "Read", "input": {}},
            {"name": "Bash", "input": {"command": "grep -r bar ."}},
        ]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 1)
        self.assertEqual(result["misses"][0]["type"], "search_miss")

    def test_rg_and_ag_detected(self):
        calls = [
            {"name": "Bash", "input": {"command": "rg pattern src/"}},
            {"name": "Bash", "input": {"command": "ag --go pattern"}},
        ]
        result = detect_misses(calls, "sense")
        self.assertEqual(result["total"], 2)


class TestParseTranscript(unittest.TestCase):
    def _write_jsonl(self, lines):
        f = tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False)
        for obj in lines:
            f.write(json.dumps(obj) + "\n")
        f.close()
        return f.name

    def test_extracts_tool_calls(self):
        lines = [
            {"event": {"type": "content_block_start", "content_block": {"type": "tool_use", "name": "Read"}}},
            {"event": {"type": "content_block_delta", "delta": {"type": "input_json_delta", "partial_json": '{"file_path":'}}},
            {"event": {"type": "content_block_delta", "delta": {"type": "input_json_delta", "partial_json": '"foo.go"}'}}},
            {"event": {"type": "content_block_stop"}},
        ]
        path = self._write_jsonl(lines)
        try:
            result = parse_transcript(path)
            self.assertEqual(len(result["tool_calls"]), 1)
            self.assertEqual(result["tool_calls"][0]["name"], "Read")
            self.assertEqual(result["tool_calls"][0]["input"]["file_path"], "foo.go")
        finally:
            os.unlink(path)

    def test_extracts_text(self):
        lines = [
            {"event": {"type": "content_block_start", "content_block": {"type": "text"}}},
            {"event": {"type": "content_block_delta", "delta": {"type": "text_delta", "text": "Hello "}}},
            {"event": {"type": "content_block_delta", "delta": {"type": "text_delta", "text": "world"}}},
            {"event": {"type": "content_block_stop"}},
        ]
        path = self._write_jsonl(lines)
        try:
            result = parse_transcript(path)
            self.assertEqual(result["final_text"], "Hello world")
        finally:
            os.unlink(path)

    def test_extracts_usage_from_result(self):
        lines = [
            {"type": "result", "total_cost_usd": 0.05, "duration_ms": 3000,
             "num_turns": 2, "usage": {
                 "input_tokens": 1000, "output_tokens": 500,
                 "cache_read_input_tokens": 80000,
                 "cache_creation_input_tokens": 5000,
             }},
        ]
        path = self._write_jsonl(lines)
        try:
            result = parse_transcript(path)
            self.assertEqual(result["usage"]["input_tokens"], 1000)
            self.assertEqual(result["usage"]["output_tokens"], 500)
            self.assertEqual(result["usage"]["cache_read_input_tokens"], 80000)
            self.assertEqual(result["usage"]["cache_creation_input_tokens"], 5000)
            self.assertEqual(result["cost_usd"], 0.05)
            self.assertEqual(result["duration_ms"], 3000)
        finally:
            os.unlink(path)

    def test_message_level_format(self):
        lines = [
            {"event": {"type": "assistant", "message": {"content": [
                {"type": "tool_use", "name": "Read", "input": {"file_path": "foo.go"}},
                {"type": "text", "text": "Here is the result."},
            ]}}},
            {"event": {"type": "user", "message": {"content": [
                {"type": "tool_result", "tool_use_id": "t1", "content": "file content"},
            ]}}},
            {"event": {"type": "assistant", "message": {"content": [
                {"type": "tool_use", "name": "mcp__sense__sense_graph",
                 "input": {"symbol": "Foo"}},
                {"type": "text", "text": "Final answer."},
            ]}}},
            {"type": "result", "total_cost_usd": 0.10, "duration_ms": 5000,
             "num_turns": 3, "usage": {
                 "input_tokens": 50, "output_tokens": 800,
                 "cache_read_input_tokens": 100000,
                 "cache_creation_input_tokens": 10000,
             }},
        ]
        path = self._write_jsonl(lines)
        try:
            result = parse_transcript(path)
            self.assertEqual(len(result["tool_calls"]), 2)
            self.assertEqual(result["tool_calls"][0]["name"], "Read")
            self.assertEqual(result["tool_calls"][1]["name"], "mcp__sense__sense_graph")
            self.assertEqual(result["final_text"], "Final answer.")
            self.assertEqual(result["usage"]["input_tokens"], 50)
            self.assertEqual(result["usage"]["cache_read_input_tokens"], 100000)
        finally:
            os.unlink(path)

    def test_empty_file(self):
        path = self._write_jsonl([])
        try:
            result = parse_transcript(path)
            self.assertEqual(result["tool_calls"], [])
            self.assertEqual(result["final_text"], "")
        finally:
            os.unlink(path)

    def test_malformed_tool_input(self):
        lines = [
            {"event": {"type": "content_block_start", "content_block": {"type": "tool_use", "name": "Bash"}}},
            {"event": {"type": "content_block_delta", "delta": {"type": "input_json_delta", "partial_json": "not valid json"}}},
            {"event": {"type": "content_block_stop"}},
        ]
        path = self._write_jsonl(lines)
        try:
            result = parse_transcript(path)
            self.assertEqual(len(result["tool_calls"]), 1)
            self.assertIn("_raw", result["tool_calls"][0]["input"])
        finally:
            os.unlink(path)


if __name__ == "__main__":
    unittest.main()
