"""Behavior tests for run_validity: a timed-out run is a RESULT, not a void."""
import run_validity as rv


def _c(rc, chars, tokens, **kw):
    return rv.classify(rc, chars, tokens, **kw)


def test_clean_run_with_an_answer_is_completed():
    r = _c(0, 34374, 23232)
    assert r["valid"] is True
    assert r["outcome"] == "completed"
    assert r["void_reason"] is None


def test_watchdog_cut_a_real_answer_stays_valid():
    """The 2026-07-21 ruling: an arm out of clock failed the exam, not the exam."""
    r = _c(124, 38649, 18994)
    assert r["valid"] is True
    assert r["outcome"] == "truncated_at_ceiling"
    assert r["watchdog_kind"] == "hard_cap_timeout"


def test_never_reaching_synthesis_is_a_real_failure_not_an_artifact():
    """Tokens and tool calls burned, 83 chars of mid-work narration: a true 0.0."""
    r = _c(124, 83, 21886)
    assert r["valid"] is True
    assert r["outcome"] == "never_reached_synthesis"


def test_watchdog_before_any_output_measures_nothing():
    r = _c(124, 0, 0)
    assert r["valid"] is False
    assert r["void_reason"] == "no_output_hang"


def test_non_watchdog_failure_is_a_harness_crash():
    """rc=1 at 188s under a 300s ceiling: the session fell over, no measurement."""
    r = _c(1, 203, 5904)
    assert r["valid"] is False
    assert r["void_reason"] == "harness_crash"


def test_clean_exit_with_a_degenerate_stream_is_invalid():
    r = _c(0, 12, 400)
    assert r["valid"] is False
    assert r["void_reason"] == "empty_final_answer"


def test_provider_cap_outranks_the_answer_length_gate():
    r = _c(0, 94, 120, provider_error=True)
    assert r["valid"] is False
    assert r["void_reason"] == "provider_cap_error"


def test_offloaded_answer_is_an_artifact():
    r = _c(0, 5000, 9000, offloaded=True)
    assert r["valid"] is False
    assert r["void_reason"] == "answer_offloaded_to_file"


def test_stall_and_cold_start_codes_are_watchdogs_too():
    assert _c(125, 9000, 5000)["watchdog_kind"] == "stalled_midrun"
    assert _c(126, 9000, 5000)["watchdog_kind"] == "no_first_output_hang"


def test_min_answer_chars_is_tunable():
    assert _c(124, 300, 5000, min_answer_chars=1000)["outcome"] == "never_reached_synthesis"
    assert _c(124, 300, 5000, min_answer_chars=200)["outcome"] == "truncated_at_ceiling"


def test_classify_run_reads_claude_evidence_from_scored_json():
    """The claude driver records no answer_chars; the scorer does."""
    meta = {"claude_exit_code": 124}
    scored = {"metrics": {"answer_chars": 38649, "token_output": 18994}}
    r = rv.classify_run(meta, scored)
    assert r["valid"] is True
    assert r["outcome"] == "truncated_at_ceiling"


def test_classify_run_prefers_run_meta_evidence_when_present():
    meta = {"opencode_exit_code": 124, "answer_chars": 22327, "output_tokens": 16435}
    r = rv.classify_run(meta, {"metrics": {"answer_chars": 0, "token_output": 0}})
    assert r["outcome"] == "truncated_at_ceiling"


def test_classify_run_carries_a_recorded_provider_cap_forward():
    meta = {"opencode_exit_code": 0, "answer_chars": 94,
            "output_tokens": 30, "error": "provider_cap_error"}
    assert rv.classify_run(meta)["void_reason"] == "provider_cap_error"


def test_classify_run_defaults_a_missing_exit_code_to_clean():
    """session-run.sh records no exit code; judge it on the answer alone."""
    r = rv.classify_run({}, {"metrics": {"answer_chars": 9000, "token_output": 4000}})
    assert r["outcome"] == "completed"


def test_parked_and_probe_dirs_are_off_board():
    assert rv.is_parked("claude-opus-4-8/dryruns-20260719/sense/pebble/run-1")
    assert rv.is_parked("claude-opus-4-8/dropped-cells-20260720/baseline/miniflux")
    assert rv.is_parked("claude-fable-5/_voided-sense-run2/run-2")
    assert rv.is_parked("claude-opus-4-8/baseline/dolt/failed-run-2-claude-session")


def test_a_normal_run_is_on_board():
    assert not rv.is_parked("claude-opus-4-8/sense/dolt/run-4")
    assert not rv.is_parked("gpt-5.5/baseline/consul/run-1")
