#!/usr/bin/env bash
# new-vertical.sh — stamp the directory structure for a new stack vertical
# (bootstrap-runbook §1). It scaffolds the MECHANICAL skeleton; it deliberately
# does NOT choose repos, pin commits, or author scenarios — those are the human
# judgment gates (bootstrap-runbook §"what new-vertical.sh must NOT automate").
#
# It stamps two homes:
#   bench/verticals/<key>/        (tracked) — scenarios/ + repos.txt membership
#   .doc/launch/<NN>-<key>-vertical/  (gitignored, local-only) — tracker + working
#     docs copied from the Rails template: README.md, repos.md, scenario-crafting.md,
#     prompts/, articles/ (00-campaign-scorecard + _skeleton + media/).
#
# Idempotent + non-destructive: every existing file is SKIPPED, never overwritten,
# so it is safe to re-run and safe against a partially-stamped vertical. Files that
# need re-aiming (README/repos.md/prompts) are copied verbatim with a TEMPLATE
# banner; re-aiming the prose for the new stack is the human/model's job.
#
# Usage:
#   bash bench/drivers/new-vertical.sh <key> [--doc-num NN] [--title "Display Name"]
#   bash bench/drivers/new-vertical.sh laravel --title "PHP / Laravel"
#   bash bench/drivers/new-vertical.sh laravel --no-doc        # bench dirs only
set -uo pipefail
cd "$(dirname "$0")/../.."                      # sense repo root
ROOT="$(pwd)"
RAILS_DOC="$ROOT/.doc/launch/02-rails-vertical"

KEY=""; DOCNUM=""; TITLE=""; NODOC=0
while [ $# -gt 0 ]; do
  case "$1" in
    --doc-num) DOCNUM="$2"; shift 2 ;;
    --title)   TITLE="$2"; shift 2 ;;
    --no-doc)  NODOC=1; shift ;;
    -h|--help) sed -n '2,24p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; exit 64 ;;
    *)  KEY="$1"; shift ;;
  esac
done
[ -z "$KEY" ] && { echo "usage: new-vertical.sh <key> [--doc-num NN] [--title T] [--no-doc]" >&2; exit 64; }
[ -z "$TITLE" ] && TITLE="$KEY"

made=0; skipped=0
mk()   { if [ -e "$1" ]; then echo "  [skip] ${1#$ROOT/}"; skipped=$((skipped+1)); else mkdir -p "$1"; echo "  [mkdir] ${1#$ROOT/}"; made=$((made+1)); fi; }
cpv()  { # src dst  — copy only if dst missing
  if [ -e "$2" ]; then echo "  [skip] ${2#$ROOT/}"; skipped=$((skipped+1)); return; fi
  [ -e "$1" ] || { echo "  [warn] template missing: ${1#$ROOT/}"; return; }
  cp "$1" "$2"; echo "  [copy] ${2#$ROOT/}  <- ${1#$ROOT/}"; made=$((made+1));
}
writef() { # path heredoc-content (stdin)  — write only if missing
  if [ -e "$1" ]; then echo "  [skip] ${1#$ROOT/}"; skipped=$((skipped+1)); cat >/dev/null; return; fi
  mkdir -p "$(dirname "$1")"; cat > "$1"; echo "  [write] ${1#$ROOT/}"; made=$((made+1));
}
cptmpl() { # src dst  — copy a Rails-template doc with structural tokens RETARGETED to
  # this stack + a generated banner, only if dst missing. Retargets the unambiguous
  # tokens (vertical key, doc-dir cross-refs, stack name); does NOT rewrite repo-/model-
  # specific examples or numbers (that would fabricate facts — those are re-aimed per
  # scenario, Loop B). $RETARGET + $BANNER are set in the doc block before this runs.
  if [ -e "$2" ]; then echo "  [skip] ${2#$ROOT/}"; skipped=$((skipped+1)); return; fi
  [ -e "$1" ] || { echo "  [warn] template missing: ${1#$ROOT/}"; return; }
  { printf '%s\n\n' "$BANNER"; sed "${RETARGET[@]}" "$1"; } > "$2"
  echo "  [gen ] ${2#$ROOT/}  <- ${1#$ROOT/} (retargeted)"; made=$((made+1))
}

echo "== stamping vertical '$KEY' (title: $TITLE) =="

# ---- 1. bench side (tracked) ------------------------------------------------
echo "-- bench/verticals/$KEY (tracked) --"
mk "$ROOT/bench/verticals/$KEY/scenarios"
writef "$ROOT/bench/verticals/$KEY/repos.txt" <<EOF
# $TITLE vertical — repo membership (one repo key per line).
# Self-contained under verticals/$KEY/: scenarios/ + results/.
# The SET is 6 repos, firm (manifesto §7.0): 1 framework + 1 big + 2 medium + 2 small
# (or 2 big + 2 medium + 2 small when the framework is too small/memorized).
# Fill in after the repo-selection gate (bootstrap-runbook §2); one key per line.
EOF

# ---- 2. .doc side (gitignored, local-only) ----------------------------------
if [ "$NODOC" = 1 ]; then
  echo "-- skipping .doc scaffold (--no-doc) --"
else
  if [ -z "$DOCNUM" ]; then
    # Reuse this key's existing dir if present (idempotency), else allocate the
    # next NN after the highest existing NN-*-vertical.
    existing="$(ls -d "$ROOT"/.doc/launch/[0-9][0-9]-"$KEY"-vertical 2>/dev/null | head -1)"
    if [ -n "$existing" ]; then
      DOCNUM="$(basename "$existing" | sed -E 's#^([0-9]{2})-.*#\1#')"
    else
      DOCNUM="$(ls -d "$ROOT"/.doc/launch/[0-9][0-9]-*-vertical 2>/dev/null \
        | sed -E 's#.*/([0-9]{2})-.*#\1#' | sort -n | tail -1)"
      DOCNUM="$(printf '%02d' "$(( 10#${DOCNUM:-02} + 1 ))")"
    fi
  fi
  DOCDIR="$ROOT/.doc/launch/$DOCNUM-$KEY-vertical"
  echo "-- $DOCDIR (gitignored) --"
  mk "$DOCDIR"
  mk "$DOCDIR/prompts"
  mk "$DOCDIR/articles/media"
  # Retarget map: structural tokens only (key, doc-dir refs, stack name). claude-opus-4-8
  # is left intact (Opus stays the headline arm for every vertical). 02-rails-vertical is
  # replaced before rails-vertical so the doc-num prefix is rewritten cleanly.
  TITLE_NS="$(printf '%s' "$TITLE" | sed 's# */ *#/#g')"   # "Python / Django" -> "Python/Django"
  RETARGET=(-e "s#02-rails-vertical#${DOCNUM}-${KEY}-vertical#g"
            -e "s#ruby-rails#${KEY}#g"
            -e "s#rails-vertical#${KEY}-vertical#g"
            -e "s#Rails-vertical#${TITLE} vertical#g"
            -e "s#Ruby/Rails#${TITLE_NS}#g"
            -e "s#Ruby / Rails#${TITLE}#g")
  BANNER="$(cat <<BNR
<!-- AUTO-GENERATED by bench/drivers/new-vertical.sh for the ${KEY} vertical, from the
     Rails template (02-rails-vertical). Structural tokens (vertical key, doc-dir paths,
     stack name) were retargeted automatically. Repo-/model-SPECIFIC examples (e.g.
     ActiveRecord::Relation, chatwoot, and specific Rails numbers) still reference Rails
     BY DESIGN; auto-rewriting them would fabricate facts, so re-aim those when you author
     each scenario (Loop B). Re-run new-vertical.sh to regenerate. -->
BNR
)"
  # stack-agnostic docs + process prompts: copy with structural retarget + banner
  cptmpl "$RAILS_DOC/scenario-crafting.md" "$DOCDIR/scenario-crafting.md"
  cptmpl "$RAILS_DOC/article-workflow.md" "$DOCDIR/article-workflow.md"
  cptmpl "$RAILS_DOC/articles/_skeleton.md" "$DOCDIR/articles/_skeleton.md"
  cptmpl "$RAILS_DOC/articles/00-campaign-scorecard.md" "$DOCDIR/articles/00-campaign-scorecard.md"
  if [ -d "$RAILS_DOC/prompts" ]; then
    for p in "$RAILS_DOC"/prompts/*.md; do
      [ -e "$p" ] && cptmpl "$p" "$DOCDIR/prompts/$(basename "$p")"
    done
  fi
  # tracker + repo-selection deliverable: small stubs pointing at the authorities
  writef "$DOCDIR/README.md" <<EOF
# $TITLE Vertical — Tracker

Vertical scaffolded by \`bench/drivers/new-vertical.sh\`. Re-aim this tracker from
the Rails one ([\`../02-rails-vertical/README.md\`](../02-rails-vertical/README.md)).

> **Authorities** (this folder never overrides them):
> [\`../00-vertical-bench-manifesto.md\`](../00-vertical-bench-manifesto.md) (rules),
> [\`../00-vertical-program.md\`](../00-vertical-program.md) (sequence),
> [\`../00-next-vertical/bootstrap-runbook.md\`](../00-next-vertical/bootstrap-runbook.md) (mechanics).

## Status

| Step | Artifact | State |
|---|---|---|
| 0 — Choose repos (6, firm) | [\`repos.md\`](repos.md) | ⬜ |
| 1 — Stamp dirs | this folder + \`bench/verticals/$KEY/\` | ✅ |
| 2 — Pin commits + freeze | \`bench/PINNED_COMMITS.json\` | ⬜ |
| 3 — Build indexes | \`bench/lib/ensure-index.sh <repo>\` | ⬜ |
| 4 — Per-repo loop | \`bench/drivers/vertical-loop.sh <repo>\` | ⬜ |

The per-repo mechanical loop is driven by \`vertical-loop.sh\`; it stops at the two
human gates (scenario authoring, tie diagnosis).
EOF
  writef "$DOCDIR/repos.md" <<EOF
# $TITLE Vertical — Repo Selection (Step 0)

The repo-selection deliverable (manifesto §1 + §7, bootstrap-runbook §2): the one
manual judgment gate the bootstrap does NOT automate. Mirror the format of
[\`../02-rails-vertical/repos.md\`](../02-rails-vertical/repos.md).

> **The SET is 6 repos, firm** (manifesto §7.0): \`1 framework + 1 big + 2 medium +
> 2 small\` (or \`2 big + 2 medium + 2 small\` when the framework is too
> small/memorized). Two independent win pillars; the discriminator picks its own
> repo; each slot carries a same-type backup (swap is the LAST resort).

## The firm 6-repo set

| Slot | Repo | Central target | Role |
|---|---|---|---|
| framework | | | win pillar 1 |
| big | | | win pillar 2 |
| medium | | | |
| medium | | | |
| small | | | control / honest tie |
| small | | | control / honest tie |

## Freeze plan (bootstrap-runbook §3 — at clone time)

\`bench/PINNED_COMMITS.json\`: for each repo \`git ls-remote <url> HEAD\` -> pin the
SHA, clone, strip any anti-LLM banner from BOTH arms, then \`bench/global/freeze-heldout.sh\`.
EOF
fi

echo ""
echo "== done: $made created, $skipped already present =="
echo "Next (bootstrap-runbook):"
echo "  2. choose the 6 repos + contracts -> ${DOCDIR:+${DOCDIR#$ROOT/}/}repos.md, fill bench/verticals/$KEY/repos.txt"
echo "  3. pin commits in bench/PINNED_COMMITS.json, clone, strip banners, freeze-heldout.sh"
echo "  4. per repo:  bash bench/drivers/vertical-loop.sh <repo>   (VERTICAL=$KEY)"
