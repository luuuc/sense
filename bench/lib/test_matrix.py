"""Behavior tests for matrix.py aggregation: artifacts out, real wall times in."""
import json
import os

import matrix


def _run(repo_dir, run, cited, meta=None, metrics=None):
    d = os.path.join(repo_dir, run)
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "scored.json"), "w") as f:
        json.dump({"gold_recall": {"cited_recall": cited},
                   "metrics": metrics or {}}, f)
    if meta is not None:
        with open(os.path.join(d, "run_meta.json"), "w") as f:
            json.dump(meta, f)
    return d


def test_a_harness_crash_is_not_averaged_into_the_arm(tmp_path):
    """The 203-char crash stub that pulled dolt from 1.00 to 0.88."""
    repo = str(tmp_path / "dolt")
    _run(repo, "run-1", 1.0, meta={"claude_exit_code": 0},
         metrics={"answer_chars": 35059, "token_output": 7392})
    _run(repo, "run-2", 0.0, meta={"claude_exit_code": 1},
         metrics={"answer_chars": 203, "token_output": 5904})
    assert matrix._arm_scores(repo)[0] == 1.0


def test_a_run_the_clock_cut_short_still_counts(tmp_path):
    """A failed exam is still an exam -- the truncation asymmetry is the finding."""
    repo = str(tmp_path / "dolt")
    _run(repo, "run-1", 1.0, meta={"claude_exit_code": 0},
         metrics={"answer_chars": 35059, "token_output": 7392})
    _run(repo, "run-2", 0.5, meta={"claude_exit_code": 124},
         metrics={"answer_chars": 17670, "token_output": 13813})
    assert matrix._arm_scores(repo)[0] == 0.75


def test_runs_without_run_meta_are_kept(tmp_path):
    repo = str(tmp_path / "dolt")
    _run(repo, "run-1", 0.5, metrics={"answer_chars": 900, "token_output": 100})
    assert matrix._arm_scores(repo)[0] == 0.5


def test_wall_time_falls_back_to_run_meta_when_the_scorer_read_zero(tmp_path):
    """codex/opencode emit no duration_ms, so scored.json carries a bogus 0.0."""
    repo = str(tmp_path / "consul")
    _run(repo, "run-1", 1.0, meta={"wall_time_seconds": 217},
         metrics={"wall_time_seconds": 0.0, "answer_chars": 9000, "token_output": 500})
    assert matrix._arm_metrics(repo)["wall_time_seconds"] == 217


def test_a_real_scorer_wall_time_is_not_overridden(tmp_path):
    repo = str(tmp_path / "consul")
    _run(repo, "run-1", 1.0, meta={"wall_time_seconds": 999},
         metrics={"wall_time_seconds": 258.0, "answer_chars": 9000, "token_output": 500})
    assert matrix._arm_metrics(repo)["wall_time_seconds"] == 258.0


def test_a_zero_wall_time_with_no_meta_is_dropped_not_averaged(tmp_path):
    repo = str(tmp_path / "consul")
    _run(repo, "run-1", 1.0, meta={},
         metrics={"wall_time_seconds": 0.0, "answer_chars": 9000, "token_output": 500})
    assert matrix._arm_metrics(repo)["wall_time_seconds"] is None


def _judged(run_dir, model):
    with open(os.path.join(run_dir, "judged.json"), "w") as f:
        json.dump({"judge": {"model": model}}, f)


def test_the_judge_is_named_from_the_data(tmp_path):
    d = _run(str(tmp_path / "dolt"), "run-1", 1.0)
    _judged(d, "claude-opus-4-7")
    para = matrix._grading_paragraph(str(tmp_path))
    assert "(claude-opus-4-7)" in para
    assert "NOT all graded by the same judge" not in para


def test_a_split_judge_is_disclosed_not_papered_over(tmp_path):
    """The go board published one judge name while two had graded it."""
    a = _run(str(tmp_path / "dolt"), "run-1", 1.0)
    b = _run(str(tmp_path / "dolt"), "run-2", 1.0)
    _judged(a, "claude-opus-4-7")
    _judged(b, "claude-sonnet-4-6")
    para = matrix._grading_paragraph(str(tmp_path))
    assert "NOT all graded by the same judge" in para
    assert "claude-opus-4-7, claude-sonnet-4-6" in para


def test_repeatability_states_the_real_spread_not_run_more_than_once():
    data = {"m": {"consul": {"runs": [2, 4]}, "dolt": {"runs": [1, 1]}}}
    assert "1x to 4x" in matrix._repeatability_sentence(data)
    assert "OPEN flag" in matrix._repeatability_sentence(data)


def test_repeatability_is_exact_when_every_arm_matches():
    data = {"m": {"consul": {"runs": [2, 2]}}}
    assert matrix._repeatability_sentence(data) == "Each (model, repo) pair was run 2x."


def test_a_dryrun_probes_judge_does_not_make_the_board_look_split(tmp_path):
    """Two sonnet-graded pebble PROBES kept the report warning about a split."""
    d = _run(str(tmp_path / "sense" / "dolt"), "run-1", 1.0)
    _judged(d, "claude-opus-4-7")
    probe = _run(str(tmp_path / "dryruns-20260719" / "sense" / "pebble"), "run-1", 1.0)
    _judged(probe, "claude-sonnet-4-6")
    assert matrix._judge_models(str(tmp_path)) == ["claude-opus-4-7"]
