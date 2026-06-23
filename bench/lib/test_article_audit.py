"""Tests for article_audit.py — the structural/referential article gate."""
import os
import textwrap

import article_audit as aa


def _write(d, name, frontmatter, body=""):
    fm = "\n".join(f"{k}: {v}" for k, v in frontmatter.items())
    open(os.path.join(d, name), "w").write(f"---\n{fm}\n---\n{body}")


def _results(tmp_path, repos):
    root = tmp_path / "res"
    for r in repos:
        (root / "sense" / r).mkdir(parents=True)
    return str(root)


FULL_BODY = "\n".join(f"## Block {b} — x\nprose\n" for b in "ABCDEFGHIJ")


def test_real_set_has_no_fail():
    """Regression guard: the live article set passes every structural check."""
    findings = aa.audit()
    fails = [f for f in findings if f[0] == aa.FAIL]
    assert fails == [], f"article set has FAIL findings: {fails}"


def test_missing_block_fails(tmp_path):
    arts = tmp_path / "articles"
    arts.mkdir()
    _write(str(arts), "01-foo.md",
           {"order": 1, "repo": "foo", "data": "x", "agents": "[]",
            "axes": "{}", "stats": "{}"},
           "## Block A — x\nonly one block\n")
    findings = aa.audit(str(arts), _results(tmp_path, ["foo"]))
    assert any(c == "blocks" and lv == aa.FAIL for lv, c, _ in findings)


def test_uncovered_repo_fails(tmp_path):
    arts = tmp_path / "articles"
    arts.mkdir()
    _write(str(arts), "01-foo.md",
           {"order": 1, "repo": "foo", "data": "x", "agents": "[]",
            "axes": "{}", "stats": "{}"}, FULL_BODY)
    # benched repos include 'bar', which has no article -> FAIL
    findings = aa.audit(str(arts), _results(tmp_path, ["foo", "bar"]))
    assert any(c == "coverage" and lv == aa.FAIL and "bar" in m
               for lv, c, m in findings)


def test_scorecard_exempt_from_blocks(tmp_path):
    """order: 0 (the 00 scorecard) is not required to carry Blocks A-I."""
    arts = tmp_path / "articles"
    arts.mkdir()
    _write(str(arts), "00-scorecard.md",
           {"order": 0, "repo": "(all)", "data": "x", "agents": "[]"},
           "no blocks here\n")
    findings = aa.audit(str(arts), _results(tmp_path, []))
    assert not any(c == "blocks" for _, c, _ in findings)


def test_clean_set_passes(tmp_path):
    arts = tmp_path / "articles"
    arts.mkdir()
    _write(str(arts), "01-foo.md",
           {"order": 1, "repo": "foo", "data": "x", "agents": "[]",
            "axes": "{}", "stats": "{}"}, FULL_BODY)
    findings = aa.audit(str(arts), _results(tmp_path, ["foo"]))
    assert [f for f in findings if f[0] == aa.FAIL] == []
