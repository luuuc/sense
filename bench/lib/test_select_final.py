"""Behavior tests for select_final: provenance-based final-benchmark selection."""
import json
import os

import select_final as sf


def _write(root, tool, repo, run, **meta):
    d = os.path.join(root, tool, repo, run)
    os.makedirs(d, exist_ok=True)
    meta.update({"tool": tool, "repo": repo})
    with open(os.path.join(d, "run_meta.json"), "w") as f:
        json.dump(meta, f)
    return d


def _fixture(root):
    V = "sha256:same"
    # sense: two clean release runs (qualify) + a dirty one + an old-release one + invalid
    _write(root, "sense", "dolt", "run-1", valid=True, sense_release="v1.1", sense_dirty=False,
           scenario_version=V, timestamp="2026-07-16T01:00:00Z")
    _write(root, "sense", "dolt", "run-2", valid=True, sense_release="v1.1", sense_dirty=False,
           scenario_version=V, timestamp="2026-07-16T02:00:00Z")
    _write(root, "sense", "dolt", "run-3", valid=True, sense_release="v1.1", sense_dirty=True,
           scenario_version=V, timestamp="2026-07-16T03:00:00Z")
    _write(root, "sense", "dolt", "run-4", valid=True, sense_release="v1.0", sense_dirty=False,
           scenario_version=V, timestamp="2026-07-16T04:00:00Z")
    _write(root, "sense", "dolt", "run-5", valid=False, sense_release="v1.1", sense_dirty=False,
           scenario_version=V, timestamp="2026-07-16T05:00:00Z")
    # baseline: version-independent, any valid qualifies
    _write(root, "baseline", "dolt", "run-1", valid=True, scenario_version=V,
           timestamp="2026-07-16T01:00:00Z")
    _write(root, "baseline", "dolt", "run-2", valid=True, scenario_version=V,
           timestamp="2026-07-16T02:00:00Z")


def test_selects_latest_two_clean_release_runs(tmp_path):
    root = str(tmp_path)
    _fixture(root)
    result = sf.select(sf._load_metas(root), "v1.1")
    sense = result[("?", "sense", "dolt")]
    picked = sorted(os.path.basename(m["_dir"]) for m in sense["selected"])
    assert picked == ["run-1", "run-2"]  # latest two clean v1.1, not the dirty run-3
    reasons = {os.path.basename(m["_dir"]): r for m, r in sense["rejected"]}
    assert reasons["run-3"] == "dirty-tree"
    assert reasons["run-4"] == "not-release"
    assert reasons["run-5"] == "invalid"


def test_baseline_is_version_independent(tmp_path):
    root = str(tmp_path)
    _fixture(root)
    result = sf.select(sf._load_metas(root), "v1.1")
    base = result[("?", "baseline", "dolt")]
    assert len(base["selected"]) == 2 and not base["rejected"]


def test_scenario_version_drift_warns(tmp_path):
    root = str(tmp_path)
    _write(root, "sense", "pebble", "run-1", valid=True, sense_release="v1.1", sense_dirty=False,
           scenario_version="sha256:A", timestamp="2026-07-16T01:00:00Z")
    _write(root, "sense", "pebble", "run-2", valid=True, sense_release="v1.1", sense_dirty=False,
           scenario_version="sha256:B", timestamp="2026-07-16T02:00:00Z")
    _, warnings = sf.report(sf.select(sf._load_metas(root), "v1.1"), root)
    assert any("scenario_version" in w for w in warnings)


def test_too_few_qualifiers_warns(tmp_path):
    root = str(tmp_path)
    _write(root, "sense", "gin", "run-1", valid=True, sense_release="v1.1", sense_dirty=False,
           scenario_version="sha256:X", timestamp="2026-07-16T01:00:00Z")
    _, warnings = sf.report(sf.select(sf._load_metas(root), "v1.1"), root)
    assert any("only 1 qualifying" in w for w in warnings)


def test_parse_and_main_end_to_end(tmp_path, capsys):
    root = str(tmp_path)
    _fixture(root)
    rc = sf.main(["select_final.py", "v1.1", "--results", root])
    out = capsys.readouterr().out
    assert "final-benchmark selection for release v1.1" in out
    assert "run-1" in out and "dirty-tree" in out
    assert rc == 0  # dolt has 2 sense + 2 baseline, no warnings


def _write_scored(run_dir, answer_chars, token_output):
    with open(os.path.join(run_dir, "scored.json"), "w") as f:
        json.dump({"metrics": {"answer_chars": answer_chars,
                               "token_output": token_output}}, f)


def test_a_timed_out_run_with_a_real_answer_still_qualifies(tmp_path):
    """The old `valid: rc == 0` stamp voided it; validity is derived now."""
    root = str(tmp_path)
    d = _write(root, "baseline", "dolt", "run-1", valid=False,
               void_reason="claude_session_failed", claude_exit_code=124,
               scenario_version="sha256:same", timestamp="2026-07-16T01:00:00Z")
    _write_scored(d, 38649, 18994)
    base = sf.select(sf._load_metas(root), "v1.1")[("?", "baseline", "dolt")]
    assert len(base["selected"]) == 1 and not base["rejected"]


def test_a_crashed_run_is_still_rejected_with_its_class(tmp_path):
    root = str(tmp_path)
    d = _write(root, "baseline", "dolt", "run-1", valid=False, claude_exit_code=1,
               scenario_version="sha256:same", timestamp="2026-07-16T01:00:00Z")
    _write_scored(d, 203, 5904)
    base = sf.select(sf._load_metas(root), "v1.1")[("?", "baseline", "dolt")]
    assert not base["selected"]
    assert base["rejected"][0][1] == "harness_crash"


def test_quarantined_runs_are_never_selected(tmp_path):
    """A run parked by renaming to `_voided-*` stays out of the selection."""
    root = str(tmp_path)
    d = _write(root, "_voided-20260721", "dolt", "run-1", valid=True,
               scenario_version="sha256:same", timestamp="2026-07-16T09:00:00Z")
    _write_scored(d, 38649, 18994)
    assert sf._load_metas(root) == []


def test_arms_are_grouped_per_model_never_across_models(tmp_path):
    """Selecting across models compared fable's sense arm to opus's baseline."""
    root = str(tmp_path)
    for model in ("claude-fable-5", "claude-opus-4-8"):
        d = _write(os.path.join(root, model), "baseline", "consul", "run-1",
                   valid=True, model=model, scenario_version="sha256:same",
                   timestamp="2026-07-16T01:00:00Z")
        _write_scored(d, 9000, 500)
    result = sf.select(sf._load_metas(root), "v1.1")
    assert ("claude-fable-5", "baseline", "consul") in result
    assert ("claude-opus-4-8", "baseline", "consul") in result
    for key in result:
        assert len(result[key]["selected"]) == 1


def test_a_confirmation_arm_at_n1_gets_an_open_flag_not_a_runs2_error(tmp_path):
    """RUNS=2 binds the headline arm only (Prime Directive 6)."""
    root = str(tmp_path)
    for model in ("claude-opus-4-8", "gpt-5.5"):
        d = _write(os.path.join(root, model), "baseline", "consul", "run-1",
                   valid=True, model=model, scenario_version="sha256:same",
                   timestamp="2026-07-16T01:00:00Z")
        _write_scored(d, 9000, 500)
    _, warnings = sf.report(sf.select(sf._load_metas(root), "v1.1"), root,
                            headline="claude-opus-4-8")
    joined = "\n".join(warnings)
    assert "claude-opus-4-8/baseline: only 1 qualifying run(s), need 2" in joined
    assert "gpt-5.5/baseline: n=1, OPEN flag" in joined


def test_a_parked_failed_run_is_not_selected_beside_its_replacement(tmp_path):
    root = str(tmp_path)
    d = _write(root, "baseline", "dolt", "failed-run-2-claude-session", valid=True,
               scenario_version="sha256:same", timestamp="2026-07-16T09:00:00Z")
    _write_scored(d, 9000, 500)
    assert sf._load_metas(root) == []


def test_an_attested_release_with_a_waived_dirty_flag_qualifies(tmp_path):
    """opencode/codex stamped no release; the maintainer attests the binary."""
    root = str(tmp_path)
    d = _write(root, "sense", "dolt", "run-1", model="gpt-5.5", sense_release="v1.1",
               sense_release_source="maintainer-attestation-2026-07-21",
               sense_dirty=True, sense_dirty_waived=True,
               scenario_version="sha256:same", timestamp="2026-07-16T01:00:00Z")
    _write_scored(d, 9000, 500)
    sense = sf.select(sf._load_metas(root), "v1.1")[("gpt-5.5", "sense", "dolt")]
    assert len(sense["selected"]) == 1 and not sense["rejected"]


def test_an_unwaived_dirty_tree_is_still_rejected(tmp_path):
    root = str(tmp_path)
    d = _write(root, "sense", "dolt", "run-1", model="gpt-5.5", sense_release="v1.1",
               sense_dirty=True, scenario_version="sha256:same",
               timestamp="2026-07-16T01:00:00Z")
    _write_scored(d, 9000, 500)
    sense = sf.select(sf._load_metas(root), "v1.1")[("gpt-5.5", "sense", "dolt")]
    assert sense["rejected"][0][1] == "dirty-tree"
