#!/usr/bin/env python3
"""Gold-target recall: did the answer surface the references a correct answer must contain?

A scenario may declare a `gold` list — the symbols / files a correct answer is
required to surface. Recall is scored against the SAME denominator for every
arm, which citation_grounding cannot do (it is grounded/emitted, a different
denominator per arm).

Two strictness levels, because they tell different stories:
  - mention_recall — the target's name/path appears at all (completeness of the
                     map). On RubyLLM both arms hit 13/13 providers here.
  - cited_recall   — the target is pinned to an exact `path:line` (precision of
                     the map: an agent can jump straight there instead of
                     grepping). This is where a structural tool pulls ahead —
                     RubyLLM baseline 3/13, sense 13/13.

Each gold item (a dict, or a bare string shorthand):
  - id:    human label, unique within the scenario
  - group: optional bucket for per-group recall (e.g. "providers")
  - match: list of case-insensitive substrings; the target is MENTIONED if any
           appears. A pattern that looks like a file (has "/" and "." or a code
           extension) also drives the CITED check: the target is cited when a
           location pin for that path appears in the answer (see _cited for the
           accepted forms). Items with no file-like pattern (pure symbol names)
           treat cited == mentioned.

The cited check accepts any of the location-pin forms agents actually use, not
just a contiguous `path:N`. The old matcher demanded the full gold path be
immediately followed by `:<line>`, which under-credited a baseline that named
the path but pinned the line a different way, and so manufactured fake
"sense-only cited" wins. The accepted forms are:
  1. `path:N`                      — gold path immediately followed by a line.
  2. `path (line N)`               — line in a trailing parenthetical.
  3. `path` ... `"line": N`        — the JSON-object form, where the line is a
                                     sibling field within a small same-object
                                     window (e.g. {"file": "...rb", "line": 5}).
  4. `<basename>:N`                — a short basename + line, e.g.
                                     `benefit.rb:260`, ONLY when that basename is
                                     unambiguous among the gold file paths (no two
                                     targets share it), so a basename pin can never
                                     double-count two distinct targets.

Returns None when the scenario declares no gold — older scenarios and
competitor runs are unaffected.
"""

import os
import re

_CODE_EXT = (".rb", ".py", ".go", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java",
             ".kt", ".cs", ".cpp", ".cc", ".c", ".php", ".scala", ".erb")

# How far past a path a sibling `"line": N` JSON field may sit and still count
# as the same object's location pin. Small enough to stay inside one object's
# adjacent fields; `{`/`}`/newline are hard stops regardless (see _cited).
_LINE_FIELD_WINDOW = 60


def _is_file_like(p):
    pl = p.lower()
    return pl.endswith(_CODE_EXT) or ("/" in p and "." in p)


def _cited(pattern, hay, basename=None):
    """Is this gold file path pinned to a line anywhere in the answer?

    `pattern` and `hay` are already lower-cased by the caller. `basename`, when
    given, is the path's unambiguous basename (unique among the gold file
    paths) and enables the short-basename form; pass None to disable it.
    """
    esc = re.escape(pattern)
    # 1. path immediately followed by a line, e.g. "anthropic.rb:24" or ":49-63".
    if re.search(esc + r":\d+", hay):
        return True
    # 2. path then a trailing parenthetical, e.g. "anthropic.rb (line 24)".
    if re.search(esc + r"\s*\(line\s+\d+\)", hay):
        return True
    # 3. path then a sibling "line": N JSON field within the same object, e.g.
    #    {"file": "...benefit.rb", "line": 260}. The [^\n{}] class keeps the
    #    match from crossing a newline or an object boundary, so it cannot bind
    #    a path to a *different* object's line field.
    if re.search(esc + r'[^\n{}]{0,%d}?"line"\s*:\s*\d+' % _LINE_FIELD_WINDOW, hay):
        return True
    # 4. unambiguous basename + line, e.g. "benefit.rb:260" when the full path
    #    was named but the line pinned via the basename. The (?<!\w) guard stops
    #    a basename from matching inside a longer sibling path (so "product.rb"
    #    does NOT match inside "line_item_product.rb").
    if basename and re.search(r'(?<!\w)' + re.escape(basename) + r":\d+", hay):
        return True
    return False


def _gold_basenames(gold):
    """Map each gold file path -> its basename IFF that basename is unique.

    A basename shared by two or more gold file paths is omitted, so the
    short-basename cited form can never credit the wrong target (or both).
    Keys and values are lower-cased to match the lower-cased haystack.
    """
    counts = {}
    paths = []
    for item in gold:
        if isinstance(item, str):
            item = {"match": [item]}
        for p in item.get("match") or []:
            sp = str(p).lower()
            if _is_file_like(sp):
                bn = os.path.basename(sp)
                paths.append((sp, bn))
                counts[bn] = counts.get(bn, 0) + 1
    return {sp: bn for sp, bn in paths if counts.get(bn, 0) == 1}


def _gold_file_targets(gold):
    """[(id, group, [lower-cased file-like patterns])] for every gold item that
    has at least one file-like `match`. Symbol-only targets (e.g. update_score)
    are excluded — precision is measured over files the agent CITED, and a bare
    method name is not a citable file endpoint.
    """
    out = []
    for item in gold:
        if isinstance(item, str):
            item = {"id": item, "match": [item]}
        pats = [str(p).lower() for p in (item.get("match") or []) if _is_file_like(str(p))]
        if pats:
            out.append((item.get("id") or pats[0], item.get("group", "_"), pats))
    return out


def _claim_matches(claimed_path, pats, unique_basenames):
    """Does a cited file path match one of a gold target's file patterns?

    `claimed_path` and `pats` are lower-cased. A match is either:
      - the gold pattern is a substring of the cited path (the common case —
        agents cite full repo-relative paths, so `reactions/x_worker.rb` ⊂
        `app/workers/reactions/x_worker.rb`), or
      - the cited path's basename equals the pattern's basename AND that
        basename is unique among the gold file targets (credits an abbreviated
        citation like `x_worker.rb` without letting a non-unique basename
        double-count two distinct targets — same guard `_cited` uses).
    """
    bc = os.path.basename(claimed_path)
    for p in pats:
        if p in claimed_path:
            return True
        bp = os.path.basename(p)
        if bp == bc and bp in unique_basenames:
            return True
    return False


def score_gold_f1(claimed_files, gold):
    """Precision / recall / F1 of the agent's claimed dependent set vs gold.

    `claimed_files` is the set of repo file paths the agent actually CITED (the
    grounded `file` of every location pin). Where `score_gold_recall` asks only
    "did the must-find targets get surfaced?", this also charges the agent for
    citing files that are NOT on the curated impact set:

      precision = |claimed ∩ gold_files| / |claimed|
      recall    = |gold_files hit by a claim| / |gold_files|   (mirrors cited_recall, file targets only)
      f1        = harmonic mean

    The point (per the sourcing runbook's "lead with precision"): a grep-driven
    baseline emits a noisy path list — its false positives cost precision here,
    which recall alone never penalises — while a structural `blast`/`graph` set
    is clean and complete. F1 is therefore grep-can't-fake in a way file-path
    recall is not.

    CAVEAT (documented, pre-registered in bench/SCORING.md): gold is the curated
    MUST-FIND set, not the complete set of every legitimately relevant file, so a
    cited file outside gold is treated as a false positive even when it is a fair
    mention. This makes precision a comparative signal between arms scored
    identically, NOT an absolute correctness measure — recall remains the
    objective floor. Returns None when the scenario declares no file-like gold.
    """
    if not gold:
        return None
    targets = _gold_file_targets(gold)
    if not targets:
        return None
    unique_basenames = set(_gold_basenames(gold).values())

    claimed, seen = [], set()
    for c in claimed_files or []:
        cl = str(c).lower()
        if cl not in seen:
            seen.add(cl)
            claimed.append(cl)

    tp, false_positives = 0, []
    for c in claimed:
        if any(_claim_matches(c, pats, unique_basenames) for _, _, pats in targets):
            tp += 1
        else:
            false_positives.append(c)
    claimed_total = len(claimed)

    hits, missed = 0, []
    for tid, _grp, pats in targets:
        if any(_claim_matches(c, pats, unique_basenames) for c in claimed):
            hits += 1
        else:
            missed.append(tid)

    precision = tp / claimed_total if claimed_total else 0.0
    recall = hits / len(targets) if targets else 0.0
    f1 = (2 * precision * recall / (precision + recall)) if (precision + recall) else 0.0
    return {
        "claimed": claimed_total,
        "gold_files": len(targets),
        "true_positives": tp,
        "false_positives": len(false_positives),
        "precision": round(precision, 4),
        "recall": round(recall, 4),
        "f1": round(f1, 4),
        "missed": missed,
        "fp_examples": false_positives[:10],
    }


def score_gold_recall(answer_text, gold):
    if not gold:
        return None
    hay = (answer_text or "").lower()
    unique_basenames = _gold_basenames(gold)
    details, groups = [], {}
    men_total = cit_total = 0
    for item in gold:
        if isinstance(item, str):
            item = {"id": item, "match": [item]}
        gid = item.get("id") or (item.get("match") or ["?"])[0]
        grp = item.get("group", "_")
        pats = item.get("match") or [gid]
        mentioned = any(str(p).lower() in hay for p in pats)
        file_pats = [str(p).lower() for p in pats if _is_file_like(str(p))]
        if file_pats:
            cited = any(_cited(p, hay, unique_basenames.get(p)) for p in file_pats)
        else:
            cited = mentioned  # pure symbol target: mention is the best we can verify
        # cited ⇒ mentioned. A target pinned only by its (unambiguous) basename
        # + line — e.g. "async_adapter.rb:50" without the full gold path — is
        # genuinely referenced, so it counts as mentioned too. Without this,
        # cited_recall could exceed mention_recall, which is incoherent (you
        # cannot cite a reference you never surfaced).
        mentioned = mentioned or cited
        men_total += 1 if mentioned else 0
        cit_total += 1 if cited else 0
        details.append({"id": gid, "group": grp, "mentioned": mentioned, "cited": cited})
        g = groups.setdefault(grp, {"total": 0, "mentioned": 0, "cited": 0})
        g["total"] += 1
        g["mentioned"] += 1 if mentioned else 0
        g["cited"] += 1 if cited else 0
    n = len(details)
    for g in groups.values():
        g["mention_recall"] = round(g["mentioned"] / g["total"], 4) if g["total"] else 0.0
        g["cited_recall"] = round(g["cited"] / g["total"], 4) if g["total"] else 0.0
    return {
        "total": n,
        "mentioned": men_total,
        "cited": cit_total,
        "mention_recall": round(men_total / n, 4) if n else 0.0,
        "cited_recall": round(cit_total / n, 4) if n else 0.0,
        "groups": groups,
        "missed_mention": [d["id"] for d in details if not d["mentioned"]],
        "missed_cite": [d["id"] for d in details if not d["cited"]],
        "details": details,
    }
