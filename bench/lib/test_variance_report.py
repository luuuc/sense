"""Behavior tests for variance_report: the page covers every model, not the last one."""
import json
import os

import variance_report as vr


def _run(root, model, arm, repo, run, cited=1.0, quality=0.8):
    d = os.path.join(root, model, arm, repo, run)
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "scored.json"), "w") as f:
        json.dump({"gold_recall": {"cited_recall": cited, "mention_recall": cited},
                   "efficiency": 0.5}, f)
    with open(os.path.join(d, "judged.json"), "w") as f:
        json.dump({"steps": [{"step_quality": quality}]}, f)
    return d


def test_every_model_that_benched_the_repo_gets_a_block(tmp_path):
    root = str(tmp_path)
    for model in ("claude-opus-4-8", "gpt-5.5"):
        for arm in ("baseline", "sense"):
            _run(root, model, arm, "consul", "run-1")
    page = vr.render(root, "consul")
    assert "## claude-opus-4-8" in page and "## gpt-5.5" in page


def test_a_model_that_did_not_bench_the_repo_is_omitted(tmp_path):
    root = str(tmp_path)
    _run(root, "claude-opus-4-8", "sense", "consul", "run-1")
    _run(root, "gpt-5.5", "sense", "dolt", "run-1")
    page = vr.render(root, "consul")
    assert "## claude-opus-4-8" in page and "## gpt-5.5" not in page


def test_the_header_states_a_range_when_run_counts_differ(tmp_path):
    root = str(tmp_path)
    _run(root, "claude-opus-4-8", "sense", "consul", "run-1")
    _run(root, "claude-opus-4-8", "sense", "consul", "run-2")
    _run(root, "gpt-5.5", "sense", "consul", "run-1")
    page = vr.render(root, "consul")
    assert "(1-2 runs per model)" in page


def test_render_returns_none_when_no_model_benched_the_repo(tmp_path):
    root = str(tmp_path)
    _run(root, "gpt-5.5", "sense", "dolt", "run-1")
    assert vr.render(root, "consul") is None


def test_repos_in_finds_every_benched_repo(tmp_path):
    root = str(tmp_path)
    _run(root, "gpt-5.5", "sense", "dolt", "run-1")
    _run(root, "claude-opus-4-8", "baseline", "consul", "run-1")
    assert vr.repos_in(root) == ["consul", "dolt"]


def test_quarantined_model_dirs_are_skipped(tmp_path):
    root = str(tmp_path)
    _run(root, "_voided-arm", "sense", "consul", "run-1")
    assert vr.render(root, "consul") is None


def test_main_writes_a_page_per_repo(tmp_path):
    root = str(tmp_path)
    _run(root, "gpt-5.5", "sense", "consul", "run-1")
    vr.main(["variance_report.py", root])
    assert os.path.exists(os.path.join(root, "variance", "consul.md"))
