"""Behavior tests for attest_release: assert a release without erasing history."""
import json
import os

import attest_release as ar


def _meta(root, model, arm, repo, **fields):
    d = os.path.join(root, model, arm, repo, "run-1")
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "run_meta.json"), "w") as f:
        json.dump(fields, f)
    return os.path.join(d, "run_meta.json")


def test_a_run_with_no_release_is_a_candidate(tmp_path):
    root = str(tmp_path)
    _meta(root, "gpt-5.5", "sense", "dolt", tool="sense")
    assert len(ar.candidates(root, "sense", [])) == 1


def test_a_self_stamped_release_is_never_a_candidate(tmp_path):
    """An opus pre-replay run on v1.12.4 must not be rewritten to the new tag."""
    root = str(tmp_path)
    _meta(root, "claude-opus-4-8", "sense", "consul", tool="sense", sense_release="v1.12.4")
    assert ar.candidates(root, "sense", []) == []


def test_a_previous_attestation_can_be_corrected(tmp_path):
    root = str(tmp_path)
    _meta(root, "gpt-5.5", "sense", "dolt", tool="sense", sense_release="v1.12.0",
          sense_release_source="maintainer-attestation-2026-01-01")
    assert len(ar.candidates(root, "sense", [])) == 1


def test_models_filter_limits_the_blast(tmp_path):
    root = str(tmp_path)
    _meta(root, "gpt-5.5", "sense", "dolt", tool="sense")
    _meta(root, "claude-fable-5", "sense", "dolt", tool="sense")
    assert len(ar.candidates(root, "sense", ["gpt-5.5"])) == 1


def test_attest_records_the_source_so_it_reads_as_asserted():
    m = ar.attest({}, "v1.13.0", "maintainer-attestation-2026-07-21", "why", False)
    assert m["sense_release"] == "v1.13.0"
    assert m["sense_release_source"] == "maintainer-attestation-2026-07-21"
    assert m["sense_release_note"] == "why"
    assert "sense_dirty_waived" not in m


def test_the_dirty_waiver_is_recorded_not_the_observation_edited():
    m = ar.attest({"sense_dirty": True}, "v1.13.0", "src", "why", True)
    assert m["sense_dirty"] is True and m["sense_dirty_waived"] is True


def test_a_clean_run_gets_no_waiver_even_when_asked():
    m = ar.attest({"sense_dirty": False}, "v1.13.0", "src", "why", True)
    assert "sense_dirty_waived" not in m


def test_dry_run_writes_nothing(tmp_path, capsys):
    root = str(tmp_path)
    p = _meta(root, "gpt-5.5", "sense", "dolt", tool="sense")
    ar.main(["attest_release.py", root, "v1.13.0", "--source", "s"])
    assert json.load(open(p)) == {"tool": "sense"}


def test_apply_writes_the_attestation(tmp_path, capsys):
    root = str(tmp_path)
    p = _meta(root, "gpt-5.5", "sense", "dolt", tool="sense")
    ar.main(["attest_release.py", root, "v1.13.0", "--source", "s", "--apply"])
    assert json.load(open(p))["sense_release"] == "v1.13.0"
