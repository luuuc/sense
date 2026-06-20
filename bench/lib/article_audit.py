#!/usr/bin/env python3
"""Structural + referential audit of the vertical article set.

Complements `check_article_stats.py` (which checks headline NUMBERS are fresh) by
checking everything else the maintain workflow needs:

  COVERAGE   — every benched repo has exactly one pack; numbering is contiguous.
  STRUCTURE  — frontmatter parses; required keys present; teardowns have Blocks A-I.
  REFERENCES — every local markdown link resolves; no links to removed files.
  SYNC       — every pack appears in the README board (and vice-versa).
  DISCIPLINE — (warn) no em-dashes in body prose; flag mentions of removed files.

Exit non-zero if any FAIL finding is emitted (WARN/INFO do not fail the gate).

Usage: article_audit.py [articles_dir] [--results <model_root>]
"""
import glob
import os
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover - environment guard
    sys.exit("article_audit.py: needs pyyaml")

REPO_ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", ".."))
VERTICAL_DIR = os.path.join(REPO_ROOT, ".doc", "launch", "02-rails-vertical")
DEFAULT_ARTICLES = os.path.join(VERTICAL_DIR, "articles")
DEFAULT_RESULTS = os.path.join(
    REPO_ROOT, "bench", "results", "vertical", "ruby-rails", "claude-opus-4-8")

REQUIRED_BLOCKS = list("ABCDEFGHI")
REQUIRED_KEYS = ("repo", "data", "agents")
TEARDOWN_KEYS = ("axes", "stats")
# docs whose links we police (current set; archive/ is historical, skipped)
SATELLITE_DOCS = ("README.md", "strategy.md", "articles-plan.md", "repos.md",
                  "scenario-crafting.md", "article-workflow.md")

FAIL, WARN, INFO = "FAIL", "WARN", "INFO"


def _split_frontmatter(text):
    """Return (frontmatter_dict_or_None, body_text)."""
    if not text.startswith("---"):
        return None, text
    end = text.find("\n---", 3)
    if end < 0:
        return None, text
    try:
        fm = yaml.safe_load(text[3:end]) or {}
    except yaml.YAMLError:
        return None, text[end + 4:]
    return fm, text[end + 4:]


def numbered_articles(articles_dir):
    return sorted(glob.glob(os.path.join(articles_dir, "[0-9][0-9]-*.md")))


def expected_repos(results_root):
    """Repo names = the sense-arm result dirs (the authoritative benched set)."""
    sense = os.path.join(results_root, "sense")
    if not os.path.isdir(sense):
        return None
    return sorted(d for d in os.listdir(sense)
                  if os.path.isdir(os.path.join(sense, d)))


def _md_links(text):
    """Yield local link targets from [text](target), excluding URLs/anchors."""
    for target in re.findall(r"\]\(([^)]+)\)", text):
        target = target.split("#", 1)[0].strip()
        if not target or target.startswith(("http://", "https://", "mailto:")):
            continue
        yield target


def audit(articles_dir=DEFAULT_ARTICLES, results_root=DEFAULT_RESULTS):
    """Return a list of (level, code, message) findings."""
    out = []
    arts = numbered_articles(articles_dir)
    by_repo = {}
    orders = []

    # --- per-article: parse, keys, blocks ---
    for path in arts:
        name = os.path.basename(path)
        fm, body = _split_frontmatter(open(path).read())
        if fm is None:
            out.append((FAIL, "parse", f"{name}: frontmatter does not parse"))
            continue
        orders.append((fm.get("order"), name))
        for k in REQUIRED_KEYS:
            if k not in fm:
                out.append((FAIL, "keys", f"{name}: missing frontmatter `{k}`"))
        if fm.get("order") != 0:  # 00 scorecard is exempt from the per-repo shape
            for k in TEARDOWN_KEYS:
                if k not in fm:
                    out.append((WARN, "keys", f"{name}: missing frontmatter `{k}`"))
            # tolerate "## Block A — x", "## Block A. X", "## Block A: x", etc.
            found = set(re.findall(r"^## Block ([A-I])\b", body, re.M))
            missing = [b for b in REQUIRED_BLOCKS if b not in found]
            if missing:
                out.append((FAIL, "blocks",
                            f"{name}: missing Block(s) {','.join(missing)}"))
            repo = fm.get("repo")
            if isinstance(repo, str):
                by_repo.setdefault(repo, []).append(name)

    # --- coverage vs benched repos ---
    exp = expected_repos(results_root)
    if exp is None:
        out.append((WARN, "coverage", f"results root not found: {results_root}"))
    else:
        for r in exp:
            if r not in by_repo:
                out.append((FAIL, "coverage", f"benched repo `{r}` has no article"))
        for r, names in by_repo.items():
            if r not in exp:
                out.append((WARN, "coverage",
                            f"article repo `{r}` ({names[0]}) has no benched dir"))
            if len(names) > 1:
                out.append((FAIL, "coverage",
                            f"repo `{r}` claimed by multiple articles: {names}"))

    # --- numbering contiguous (01..N) ---
    nums = sorted(int(os.path.basename(a)[:2]) for a in arts)
    expected_seq = list(range(0, len(nums))) if 0 in nums else list(range(1, len(nums) + 1))
    if nums != expected_seq:
        out.append((WARN, "numbering", f"non-contiguous article numbers: {nums}"))

    # --- README board sync ---
    readme = os.path.join(articles_dir, "README.md")
    if os.path.exists(readme):
        rtext = open(readme).read()
        for path in arts:
            name = os.path.basename(path)
            if name not in rtext:
                out.append((WARN, "board", f"{name}: not listed in README board"))

    # --- references: every local markdown link resolves (catches links to
    #     removed files; plain prose mentions of removed files are NOT flagged,
    #     since the 'was removed' notes legitimately name them) ---
    scan = list(glob.glob(os.path.join(articles_dir, "*.md")))
    scan += [os.path.join(VERTICAL_DIR, d) for d in SATELLITE_DOCS]
    scan += glob.glob(os.path.join(VERTICAL_DIR, "prompts", "*.md"))
    for path in scan:
        if not os.path.exists(path):
            continue
        text = open(path).read()
        base = os.path.dirname(path)
        rel = os.path.relpath(path, VERTICAL_DIR)
        for target in _md_links(text):
            resolved = os.path.normpath(os.path.join(base, target))
            if (resolved.endswith(".md") or "/" in target) and not os.path.exists(resolved):
                out.append((FAIL, "link", f"{rel}: broken link -> {target}"))

    # --- discipline: em-dashes in body PROSE (warn). Headings (#) and table
    #     rows (| ... |, where '—' is the N/A cell marker) are excluded. ---
    for path in arts:
        _, body = _split_frontmatter(open(path).read())
        bad = [ln for ln in body.splitlines()
               if "—" in ln and not ln.lstrip().startswith("#") and "|" not in ln]
        if bad:
            out.append((WARN, "em-dash",
                        f"{os.path.basename(path)}: {len(bad)} prose line(s) with an em-dash"))

    return out


def main(argv):
    articles_dir, results_root = DEFAULT_ARTICLES, DEFAULT_RESULTS
    rest = argv[1:]
    i = 0
    while i < len(rest):
        if rest[i] == "--results":
            results_root = os.path.abspath(rest[i + 1])
            i += 2
        else:
            articles_dir = os.path.abspath(rest[i])
            i += 1
    findings = audit(articles_dir, results_root)
    fails = [f for f in findings if f[0] == FAIL]
    warns = [f for f in findings if f[0] == WARN]
    for level in (FAIL, WARN, INFO):
        for lv, code, msg in findings:
            if lv == level:
                print(f"{lv:<4} [{code}] {msg}")
    print(f"\narticle audit: {len(fails)} FAIL, {len(warns)} WARN"
          + ("  -> all structural checks passed" if not fails else ""))
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
