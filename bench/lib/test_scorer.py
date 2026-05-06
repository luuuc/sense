#!/usr/bin/env python3
"""Tests for scorer.py — pure functions only, no file I/O."""

import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(__file__))

from scorer import (
    _extract_class_prefix,
    _keyword_matches,
    _strip_class_qualifier,
    _strip_python_module_prefix,
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

    # --- PascalCase class qualifier preserved in normalize ---

    def test_bare_preserves_class_method_dot(self):
        self.assertEqual(normalize_caller("Server.renderToHTML"), "Server.renderToHTML")

    def test_bare_preserves_class_method_hash(self):
        self.assertEqual(normalize_caller("Flask#wsgi_app"), "Flask#wsgi_app")

    def test_bare_go_then_class_preserved(self):
        self.assertEqual(normalize_caller("gin.Engine.ServeHTTP"), "Engine.ServeHTTP")

    # --- Symbol:filepath format (card 1 fix) ---

    def test_symbol_filepath_ts(self):
        self.assertEqual(
            normalize_caller("renderToHTMLOrFlight:packages/next/src/server/app-render/app-render.tsx"),
            "renderToHTMLOrFlight",
        )

    def test_symbol_filepath_rb(self):
        self.assertEqual(
            normalize_caller("PostCreator:app/models/post_creator.rb"),
            "PostCreator",
        )

    def test_symbol_filepath_bare_go(self):
        self.assertEqual(normalize_caller("HandleRequest:main.go"), "HandleRequest")

    def test_symbol_filepath_uppercase(self):
        self.assertEqual(normalize_caller("Flask:src/flask/app.py"), "Flask")

    def test_symbol_filepath_go_package_stripped(self):
        self.assertEqual(normalize_caller("gin.Engine:gin.go"), "Engine")

    def test_filepath_symbol_preserved(self):
        self.assertEqual(
            normalize_caller("packages/next/src/module.ts:AppPageRouteModule.render"),
            "packages/next/src/module.ts:AppPageRouteModule.render",
        )

    def test_filepath_symbol_rb_preserved(self):
        self.assertEqual(
            normalize_caller("app/models/post_creator.rb:PostCreator"),
            "app/models/post_creator.rb:PostCreator",
        )

    def test_symbol_filepath_method_dot(self):
        self.assertEqual(
            normalize_caller("PostCreator.create:app/models/post_creator.rb"),
            "PostCreator.create",
        )

    def test_symbol_filepath_method_hash(self):
        self.assertEqual(
            normalize_caller("PostCreator#new_topic?:lib/post_creator.rb"),
            "PostCreator#new_topic?",
        )

    # --- Ruby :: namespace stripping ---

    def test_ruby_nested_namespace(self):
        self.assertEqual(
            normalize_caller("app/controllers/pickups_controller.rb:Admin::Orders::PickupsController"),
            "app/controllers/pickups_controller.rb:PickupsController",
        )

    def test_ruby_namespace_with_method(self):
        self.assertEqual(
            normalize_caller("app/controllers/pickups_controller.rb:42 Admin::Orders::PickupsController#index"),
            "app/controllers/pickups_controller.rb:PickupsController#index",
        )

    def test_ruby_single_namespace(self):
        self.assertEqual(
            normalize_caller("app/models/post.rb:42 Blog::Post"),
            "app/models/post.rb:Post",
        )

    # --- Python module prefix stripping ---

    def test_python_module_bare(self):
        self.assertEqual(normalize_caller("flask.app._make_timedelta"), "_make_timedelta")

    def test_python_module_file_attached(self):
        self.assertEqual(
            normalize_caller("src/flask/app.py:flask.app._make_timedelta"),
            "src/flask/app.py:_make_timedelta",
        )

    def test_python_module_space_separated(self):
        self.assertEqual(
            normalize_caller("src/flask/app.py:42 flask.app._make_timedelta"),
            "src/flask/app.py:_make_timedelta",
        )

    def test_python_module_uppercase_final(self):
        self.assertEqual(normalize_caller("flask.app.Flask"), "Flask")

    def test_python_single_segment_not_stripped(self):
        self.assertEqual(normalize_caller("node.getValue"), "node.getValue")

    # --- Go regression tests ---

    def test_go_package_still_stripped(self):
        self.assertEqual(
            normalize_caller("internal/scan/scan.go:42 scan.Scanner"),
            "internal/scan/scan.go:Scanner",
        )

    def test_go_bare_still_stripped(self):
        self.assertEqual(normalize_caller("gin.Engine"), "Engine")

    def test_go_type_method_preserved(self):
        self.assertEqual(
            normalize_caller("internal/scan/scan.go:Scanner.Run"),
            "internal/scan/scan.go:Scanner.Run",
        )


class TestExtractClassPrefix(unittest.TestCase):
    def test_dot_method(self):
        self.assertEqual(_extract_class_prefix("PostCreator.create"), "PostCreator")

    def test_hash_method(self):
        self.assertEqual(_extract_class_prefix("PostCreator#new_topic?"), "PostCreator")

    def test_bare_class_no_prefix(self):
        self.assertIsNone(_extract_class_prefix("PostCreator"))

    def test_file_colon_class_method(self):
        self.assertEqual(
            _extract_class_prefix("lib/post_creator.rb:PostCreator#create_topic"),
            "PostCreator",
        )

    def test_go_type_method(self):
        self.assertEqual(_extract_class_prefix("Engine.ServeHTTP"), "Engine")

    def test_no_separator(self):
        self.assertIsNone(_extract_class_prefix("renderToHTMLOrFlight"))

    def test_over_broad_guard(self):
        self.assertEqual(
            _extract_class_prefix("PostGuardian#can_create_post?"),
            "PostGuardian",
        )

    def test_ruby_namespace_with_method(self):
        self.assertEqual(
            _extract_class_prefix("Admin::Orders::PickupsController#index"),
            "PickupsController",
        )

    def test_ruby_namespace_dot_method(self):
        self.assertEqual(
            _extract_class_prefix("Admin::PickupsController.create"),
            "PickupsController",
        )

    def test_ruby_namespace_no_method(self):
        self.assertIsNone(_extract_class_prefix("Admin::PickupsController"))


class TestStripPythonModulePrefix(unittest.TestCase):
    def test_two_segments(self):
        self.assertEqual(_strip_python_module_prefix("flask.app._make_timedelta"), "_make_timedelta")

    def test_three_segments(self):
        self.assertEqual(_strip_python_module_prefix("werkzeug.routing.map.Map"), "Map")

    def test_single_segment_not_stripped(self):
        self.assertEqual(_strip_python_module_prefix("flask.Flask"), "flask.Flask")

    def test_go_style_not_stripped(self):
        self.assertEqual(_strip_python_module_prefix("gin.Engine"), "gin.Engine")

    def test_no_dots(self):
        self.assertEqual(_strip_python_module_prefix("Flask"), "Flask")

    def test_uppercase_prefix_not_stripped(self):
        self.assertEqual(_strip_python_module_prefix("Flask.app.run"), "Flask.app.run")


class TestScoreSetMatchPartialCredit(unittest.TestCase):
    def test_class_method_matches_class(self):
        response = {"symbols": ["PostCreator.create"]}
        gt = {"symbols": ["PostCreator"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 1)
        self.assertEqual(result["partial_matches"][0]["found"], "PostCreator.create")
        self.assertEqual(result["partial_matches"][0]["expected"], "PostCreator")
        self.assertAlmostEqual(result["precision"], 0.5)
        self.assertAlmostEqual(result["recall"], 0.5)

    def test_hash_method_matches_class(self):
        response = {"symbols": ["TopicCreator#create"]}
        gt = {"symbols": ["TopicCreator"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 1)
        self.assertAlmostEqual(result["f1"], 0.5)

    def test_over_broad_no_match(self):
        response = {"symbols": ["PostGuardian#can_create_post?"]}
        gt = {"symbols": ["Post"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 0)
        self.assertEqual(result["f1"], 0.0)

    def test_n_to_one_pairing(self):
        response = {"symbols": ["PostCreator.create", "PostCreator#new_topic?"]}
        gt = {"symbols": ["PostCreator"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 2)
        self.assertEqual(len(result["false_positives"]), 0)

    def test_exact_match_preferred_over_partial(self):
        response = {"symbols": ["PostCreator", "PostCreator.create"]}
        gt = {"symbols": ["PostCreator"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(result["true_positives"], ["PostCreator"])
        self.assertEqual(len(result["partial_matches"]), 0)

    def test_no_partial_when_no_separator(self):
        response = {"symbols": ["Foo", "Bar"]}
        gt = {"symbols": ["Baz"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 0)

    def test_file_symbol_partial_with_bare_gt(self):
        response = {"callers": ["lib/post_creator.rb:PostCreator#create_topic"]}
        gt = {"callers": ["PostCreator"]}
        result = score_set_match(response, gt, "callers")
        self.assertEqual(len(result["partial_matches"]), 1)

    def test_reverse_partial_response_less_specific(self):
        response = {"symbols": ["PostCreator"]}
        gt = {"symbols": ["PostCreator#create"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 1)
        self.assertEqual(result["partial_matches"][0]["found"], "PostCreator")
        self.assertEqual(result["partial_matches"][0]["expected"], "PostCreator#create")
        self.assertAlmostEqual(result["f1"], 0.5)

    def test_reverse_partial_dot_method(self):
        response = {"symbols": ["Engine"]}
        gt = {"symbols": ["Engine.ServeHTTP"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 1)

    def test_bidirectional_both_directions(self):
        response = {"symbols": ["A.method1", "B"]}
        gt = {"symbols": ["A", "B#method2"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 2)
        self.assertEqual(len(result["false_positives"]), 0)
        self.assertEqual(len(result["false_negatives"]), 0)

    def test_class_method_matches_bare_method(self):
        response = {"symbols": ["Server.renderToHTML"]}
        gt = {"symbols": ["renderToHTML"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 1)
        self.assertAlmostEqual(result["f1"], 0.5)

    def test_bare_method_matches_class_method(self):
        response = {"symbols": ["wsgi_app"]}
        gt = {"symbols": ["Flask.wsgi_app"]}
        result = score_set_match(response, gt, "symbols")
        self.assertEqual(len(result["partial_matches"]), 1)
        self.assertAlmostEqual(result["f1"], 0.5)


class TestIntegrationNormalization(unittest.TestCase):
    def test_discourse_callers_ruby_namespace(self):
        response = {"callers": [
            "lib/post_creator.rb:PostCreator#create!",
            "lib/post_creator.rb:PostCreator#create_topic",
            "lib/topic_creator.rb:TopicCreator#create_shared_draft",
            "plugins/chat/lib/chat/channel_archive_service.rb:Chat::ChannelArchiveService#create_post",
        ]}
        gt = {"callers": [
            "lib/post_creator.rb:PostCreator#create!",
            "lib/post_creator.rb:PostCreator#create_topic",
            "lib/topic_creator.rb:TopicCreator#create_shared_draft",
            "plugins/chat/lib/chat/channel_archive_service.rb:ArchiveValidationError#create_post",
        ]}
        result = score_set_match(response, gt, "callers")
        self.assertEqual(len(result["true_positives"]), 3)
        self.assertEqual(len(result["partial_matches"]), 0)
        self.assertEqual(len(result["false_positives"]), 1)
        self.assertEqual(len(result["false_negatives"]), 1)

    def test_discourse_callers_response_less_specific(self):
        response = {"callers": [
            "lib/post_creator.rb:PostCreator",
            "lib/topic_creator.rb:TopicCreator",
        ]}
        gt = {"callers": [
            "lib/post_creator.rb:PostCreator#create!",
            "lib/topic_creator.rb:TopicCreator#create_shared_draft",
        ]}
        result = score_set_match(response, gt, "callers")
        self.assertEqual(len(result["true_positives"]), 0)
        self.assertEqual(len(result["partial_matches"]), 2)
        self.assertAlmostEqual(result["f1"], 0.5)

    def test_flask_dead_code_python_module_prefix(self):
        response = {"dead_symbols": [
            "flask.helpers._called_with_wrong_args",
            "flask.logging.has_level_handler",
            "FlaskGroup",
        ]}
        gt = {"dead_symbols": [
            "_called_with_wrong_args",
            "has_level_handler",
            "FlaskGroup",
        ]}
        result = score_set_match(response, gt, "dead_symbols")
        self.assertEqual(len(result["true_positives"]), 3)
        self.assertEqual(result["f1"], 1.0)

    def test_gin_dead_code_go_prefix(self):
        response = {"dead_symbols": [
            "gin.BasicAuth",
            "gin.CustomRecovery",
            "gin.ErrorLogger",
            "WrapF",
        ]}
        gt = {"dead_symbols": [
            "BasicAuth",
            "CustomRecovery",
            "ErrorLogger",
            "WrapF",
            "WrapH",
        ]}
        result = score_set_match(response, gt, "dead_symbols")
        self.assertEqual(len(result["true_positives"]), 4)
        self.assertAlmostEqual(result["precision"], 1.0)
        self.assertAlmostEqual(result["recall"], 4 / 5)


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


class TestStrippedVsStrippedPartialMatch(unittest.TestCase):
    def test_same_method_different_dot_class_same_file(self):
        """Config.determineUri vs MicrometerPlugin.determineUri — same file, same method."""
        response = {"affected": ["src/Plugin.kt:MicrometerPlugin.determineUri"]}
        gt = {"affected": ["src/Plugin.kt:Config.determineUri"]}
        result = score_set_match(response, gt, "affected")
        self.assertEqual(len(result["partial_matches"]), 1)

    def test_same_method_different_dot_class_different_file(self):
        """Different files — no partial match for stripped-vs-stripped."""
        response = {"affected": ["a.kt:Foo.run"]}
        gt = {"affected": ["b.kt:Bar.run"]}
        result = score_set_match(response, gt, "affected")
        self.assertEqual(len(result["partial_matches"]), 0)

    def test_ruby_hash_qualifier_no_stripped_match(self):
        """Ruby # qualifier — class name is semantic, don't match stripped-vs-stripped."""
        response = {"callers": ["svc.rb:ChannelArchiveService#create_post"]}
        gt = {"callers": ["svc.rb:ArchiveValidationError#create_post"]}
        result = score_set_match(response, gt, "callers")
        self.assertEqual(len(result["partial_matches"]), 0)

    def test_dotted_module_prefix_match(self):
        """server.response-cache.web.WebResponseCache matches bare WebResponseCache."""
        response = {"dead_symbols": ["server.response-cache.web.WebResponseCache"]}
        gt = {"dead_symbols": ["WebResponseCache"]}
        result = score_set_match(response, gt, "dead_symbols")
        self.assertEqual(len(result["partial_matches"]), 1)

    def test_dotted_prefix_both_sides(self):
        """Both sides have dotted prefixes stripping to same bare name."""
        response = {"dead_symbols": ["server.response-cache.WebResponseCache"]}
        gt = {"dead_symbols": ["next.server.WebResponseCache"]}
        result = score_set_match(response, gt, "dead_symbols")
        self.assertEqual(len(result["partial_matches"]), 1)


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

    def test_word_proximity_match(self):
        text = "The Engine serves as the central router for all HTTP requests"
        gt = {"keywords": ["Engine as central router"]}
        result = score_keyword_presence(text, gt, "keywords")
        self.assertEqual(result["score"], 1.0)
        self.assertEqual(result["found_keywords"], ["Engine as central router"])

    def test_word_proximity_no_match_when_words_far_apart(self):
        text = ("The Engine handles startup. " + "x" * 300 +
                " The central router is elsewhere.")
        gt = {"keywords": ["Engine as central router"]}
        result = score_keyword_presence(text, gt, "keywords")
        self.assertEqual(result["score"], 0.0)

    def test_word_proximity_single_significant_word_no_fallback(self):
        # "HTTP web framework" has only 2 significant words (>3 chars):
        # "framework" — wait, "HTTP" is 4 chars so it counts too.
        # Single significant word keywords should NOT use proximity.
        text = "This is a web application"
        gt = {"keywords": ["web"]}
        result = score_keyword_presence(text, gt, "keywords")
        self.assertEqual(result["score"], 1.0)  # exact match works


class TestKeywordMatches(unittest.TestCase):
    def test_exact_match(self):
        self.assertTrue(_keyword_matches("tree-sitter", "uses tree-sitter for parsing"))

    def test_exact_match_case_insensitive(self):
        self.assertTrue(_keyword_matches("SQLite", "uses sqlite storage"))

    def test_proximity_match(self):
        self.assertTrue(_keyword_matches(
            "Engine as central router",
            "the engine serves as the central router for requests",
        ))

    def test_proximity_no_match_far_apart(self):
        text = "the engine handles startup. " + "x" * 300 + " the central router is here."
        self.assertFalse(_keyword_matches("Engine as central router", text))

    def test_single_significant_word_exact_only(self):
        # "large hub" has one word >3 chars ("large") — not enough for proximity.
        # And "large hub" is not a substring of the text, so no exact match either.
        self.assertFalse(_keyword_matches("large hub", "this is a large server application"))

    def test_no_significant_words(self):
        # All words <= 3 chars — no proximity fallback, exact only
        self.assertFalse(_keyword_matches("foo bar", "contains foo and bar separately"))

    def test_proximity_within_window(self):
        text = "the context carries the request state efficiently"
        self.assertTrue(_keyword_matches("Context carries request state", text))


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
