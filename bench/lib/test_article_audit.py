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


# --- _skeleton.md embedded-board validation ---

# canonical board (as scoreboard.build would return it) for the fixtures below
_CANON = {
    "repos": {
        "mastodon": {"cited_b": 0.28, "cited_s": 0.83, "dd": 0.72,
                     "sense_only": 12, "verdict": "WIN", "antifab": False},
        "discourse": {"cited_b": 0.35, "cited_s": 0.75, "dd": 0.56,
                      "sense_only": 10, "verdict": "WIN", "antifab": True},
        "ruby_llm": {"cited_b": 0.76, "cited_s": 0.80, "dd": None,
                     "sense_only": 0, "verdict": "WIN", "antifab": False},
    },
    "wins": 3, "ties": 0, "losses": 0, "mean_d": 0.470, "n": 3,
    "antifab": ["discourse"],
}

_SKEL_OK = (
    "**3 wins / 0 ties / 0 losses** across 3 repos on `cited_recall`. "
    "Mean cited Δ (sense − baseline): **+0.470**. One **anti-fabrication** "
    "wins (⚑ — baseline asserts a wrong relation): **discourse**.\n\n"
    "| repo | cited recall b→s (Δ) | deps-delta | sense-only | B | related | grnd | contra | verdict |\n"
    "|---|---|---|---|---|---|---|---|---|\n"
    "| mastodon | 0.28→0.83 (+0.55) | +0.72 | 12 | x | x | x | x | **WIN** |\n"
    "| discourse | 0.35→0.75 (+0.40) | +0.56 | 10 | x | x | x | x | **WIN ⚑** |\n"
    "| ruby_llm 🔸 | 0.76→0.80 (+0.04) | — | 0 | x | x | x | x | **WIN** |\n"
)


def _skel(d, text):
    open(os.path.join(d, "_skeleton.md"), "w").write(text)


def _skel_fails(tmp_path, text):
    arts = tmp_path / "articles"
    arts.mkdir()
    _skel(str(arts), text)
    findings = aa.skeleton_board_findings(str(arts), "", canon=_CANON)
    return [m for lv, c, m in findings if lv == aa.FAIL]


def test_skeleton_matching_board_passes(tmp_path):
    assert _skel_fails(tmp_path, _SKEL_OK) == []


def test_skeleton_headline_drift_fails(tmp_path):
    bad = _SKEL_OK.replace("**3 wins / 0 ties", "**4 wins / 0 ties")
    fails = _skel_fails(tmp_path, bad)
    assert any("headline" in m for m in fails)


def test_skeleton_mean_delta_drift_fails(tmp_path):
    bad = _SKEL_OK.replace("**+0.470**", "**+0.310**")
    fails = _skel_fails(tmp_path, bad)
    assert any("mean cited" in m for m in fails)


def test_skeleton_row_cell_drift_fails(tmp_path):
    bad = _SKEL_OK.replace("| 12 |", "| 9 |")  # mastodon sense-only 12 -> 9
    fails = _skel_fails(tmp_path, bad)
    assert any("mastodon" in m and "sense-only" in m for m in fails)


def test_skeleton_verdict_drift_fails(tmp_path):
    bad = _SKEL_OK.replace("| **WIN ⚑** |", "| **TIE** |")  # discourse WIN -> TIE
    fails = _skel_fails(tmp_path, bad)
    assert any("discourse" in m and "verdict" in m for m in fails)


def test_skeleton_missing_row_fails(tmp_path):
    bad = "\n".join(l for l in _SKEL_OK.splitlines() if not l.startswith("| ruby_llm"))
    fails = _skel_fails(tmp_path, bad)
    assert any("missing rows" in m and "ruby_llm" in m for m in fails)


def test_skeleton_absent_is_warn_not_fail(tmp_path):
    arts = tmp_path / "articles"
    arts.mkdir()  # no _skeleton.md written
    findings = aa.skeleton_board_findings(str(arts), "", canon=_CANON)
    assert [f for f in findings if f[0] == aa.FAIL] == []
    assert any(c == "skeleton" and lv == aa.WARN for lv, c, _ in findings)
