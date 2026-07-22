#!/usr/bin/env python3
"""run_validity.py -- one classifier for "is this run a MEASUREMENT?", shared by
every runner and by select_final.

`valid` answers ONE question: did this run measure the arm? It never answers
"did the arm do well". An arm that runs out of wall clock FAILED the exam, and
the exam still counts (a standing rule; full rule in
scorer.py -> TIME_CEILINGS). Collapsing those two questions into `rc == 0` is
what made a 38,649-char timed-out baseline read as "no result".

Same watchdog exit code, OPPOSITE meanings -- the tell is the answer text, not
the exit code:

  completed              rc 0, a real answer                    -> VALID
  truncated_at_ceiling   watchdog cut a real answer short       -> VALID (failed exam)
  never_reached_synthesis tokens+tool calls burned, no answer   -> VALID (real 0.0)
  empty_final_answer     clean exit, degenerate/empty stream    -> invalid
  no_output_hang         watchdog cut it before any output      -> invalid
  provider_cap_error     metered sub refused mid-delivery       -> invalid
  answer_offloaded       answer written to a file, stub returned-> invalid
  harness_crash          non-watchdog failure                   -> invalid

The four invalid classes are measurement ARTIFACTS: the harness, not the arm,
decided the outcome, so they are re-run rather than scored.
"""
import glob
import json
import os

# Watchdog exits: the runner's own clock stopped the session. Everything else
# non-zero is the harness falling over, which measures nothing.
WATCHDOG_CODES = {
    124: "hard_cap_timeout",
    125: "stalled_midrun",
    126: "no_first_output_hang",
}

# Final assistant text shorter than this is mid-work narration, not an answer.
MIN_ANSWER_CHARS = 200

_ARTIFACTS = {
    "empty_final_answer",
    "no_output_hang",
    "provider_cap_error",
    "answer_offloaded_to_file",
    "harness_crash",
}


def classify(rc, answer_chars, output_tokens,
             provider_error=False, offloaded=False,
             min_answer_chars=MIN_ANSWER_CHARS):
    """Return {"valid", "outcome", "void_reason", "watchdog_kind"} for one run.

    answer_chars is the FINAL assistant text length; output_tokens is what the
    session generated. Their ratio is the classification -- see the module
    docstring. rc alone is never the verdict.
    """
    rc = int(rc)
    answer_chars = int(answer_chars or 0)
    output_tokens = int(output_tokens or 0)
    watchdog_kind = WATCHDOG_CODES.get(rc)

    outcome = _outcome(rc, answer_chars, output_tokens,
                       provider_error, offloaded, min_answer_chars,
                       watchdog_kind is not None)
    valid = outcome not in _ARTIFACTS
    return {
        "valid": valid,
        "outcome": outcome,
        "void_reason": None if valid else outcome,
        "watchdog_kind": watchdog_kind,
    }


def _outcome(rc, answer_chars, output_tokens,
             provider_error, offloaded, min_answer_chars, is_watchdog):
    """Which of the eight classes this run landed in."""
    # Provider cap first: its error blob is short enough to also trip the
    # answer-length gate, but the cap is the actionable diagnosis.
    if provider_error:
        return "provider_cap_error"
    if offloaded:
        return "answer_offloaded_to_file"
    if is_watchdog:
        if output_tokens == 0:
            return "no_output_hang"
        # Tokens were spent. A long answer means the clock cut a real delivery;
        # a short one means the arm never got to the answer at all. Both are the
        # arm's result, not the instrument's failure.
        return ("truncated_at_ceiling" if answer_chars >= min_answer_chars
                else "never_reached_synthesis")
    if rc != 0:
        return "harness_crash"
    return ("completed" if answer_chars >= min_answer_chars
            else "empty_final_answer")


def classify_run(meta, scored=None, min_answer_chars=MIN_ANSWER_CHARS):
    """Classify from a run's on-disk records, newest evidence first.

    Reads the exit code and answer evidence out of run_meta.json (`meta`) and,
    when the runner did not record them there, out of the sibling scored.json
    (`scored`). This derives the class from what the run LEFT BEHIND, so a run
    stamped by an older driver reclassifies correctly with no rewrite.
    """
    scored = scored or {}
    metrics = scored.get("metrics") or {}
    rc = _first(meta, ("claude_exit_code", "codex_exit_code", "opencode_exit_code"))
    answer_chars = _first(meta, ("answer_chars",))
    if answer_chars is None:
        answer_chars = metrics.get("answer_chars")
    output_tokens = _first(meta, ("output_tokens",))
    if output_tokens is None:
        output_tokens = metrics.get("token_output")
    error = meta.get("error")

    # No answer evidence anywhere (an unscored run, or a runner that records
    # neither): there is nothing to classify FROM, so defer to whatever the
    # runner stamped rather than inventing a verdict from silence.
    if answer_chars is None:
        return _from_stored_flag(meta)
    return classify(
        rc if rc is not None else 0,
        answer_chars, output_tokens,
        provider_error=(error == "provider_cap_error"),
        offloaded=(error == "answer_offloaded_to_file"),
        min_answer_chars=min_answer_chars,
    )


# Directory-name conventions for runs that are ON DISK but NOT part of the
# published board: maintainer-parked (`_voided-*`, `_invalid-*`), superseded
# (`failed-run-*`), and pre-campaign probes (`dryruns-*`, `dropped-cells-*`).
# One list, because "which runs are published" was being answered differently by
# select_final (which had started selecting dryrun probes) and by the report
# generator (which counted a dryrun's judge model as a board-wide judge split).
_PARKED_PREFIXES = ("_", "failed-", "dryruns-", "dropped-cells-")


def is_parked(rel_path):
    """True if any path segment marks this run as off-board."""
    return any(seg.startswith(_PARKED_PREFIXES)
               for seg in rel_path.replace(os.sep, "/").split("/"))


def measured_runs(repo_dir):
    """This arm's scored.json paths, MEASUREMENT runs only.

    The single answer to "which runs count", shared by every instrument that
    publishes a number. Three instruments used to carry their own copy of this
    glob -- matrix.py, scoreboard.py and check_article_stats.py -- and when one
    learned to drop measurement artifacts and the others did not, they published
    two different headlines for the same cell (dolt +0.50 vs +0.38, because a
    203-char harness crash was averaged in by two of the three).
    """
    paths = sorted(glob.glob(os.path.join(repo_dir, "run-*", "scored.json")))
    if not paths and os.path.exists(os.path.join(repo_dir, "scored.json")):
        paths = [os.path.join(repo_dir, "scored.json")]
    return [p for p in paths if measured(p)]


def measured(scored_path):
    """True unless this run is a measurement artifact."""
    run_dir = os.path.dirname(scored_path)
    meta_path = os.path.join(run_dir, "run_meta.json")
    if not os.path.exists(meta_path):
        return True
    try:
        with open(meta_path) as f:
            meta = json.load(f)
        with open(scored_path) as f:
            scored = json.load(f)
    except (OSError, ValueError):
        return True
    return classify_run(meta, scored)["valid"]


def _from_stored_flag(meta):
    """Fall back to the runner's own stamp when the run left no answer evidence.

    An unstamped run is treated as a measurement: `valid` was absent from the
    opencode and session runners entirely, and defaulting those to invalid
    silently deleted four whole arms from the final selection.
    """
    valid = meta.get("valid")
    if valid is None:
        valid = True
    reason = meta.get("void_reason") or meta.get("error") or "invalid"
    return {
        "valid": bool(valid),
        "outcome": "unclassified" if valid else reason,
        "void_reason": None if valid else reason,
        "watchdog_kind": WATCHDOG_CODES.get(int(meta.get("claude_exit_code") or 0)),
    }


def _first(meta, keys):
    """First key present in meta with a non-None value."""
    for k in keys:
        if meta.get(k) is not None:
            return meta[k]
    return None
