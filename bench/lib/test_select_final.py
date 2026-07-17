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
    sense = result[("sense", "dolt")]
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
    base = result[("baseline", "dolt")]
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
