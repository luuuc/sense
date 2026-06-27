#!/usr/bin/env bash
# provision-repos.sh — clone-day provisioning for a vertical (bootstrap-runbook §3/§4).
# For each repo: shallow-fetch the PINNED_COMMITS SHA into BOTH arms (baseline + sense),
# scan for an anti-LLM banner (the lobsters fairness rule), and optionally build the
# sense-arm index. Matches the existing depth-1 clone layout (commit count = 1).
#
# Idempotent: a clone already checked out at the pinned SHA is skipped; re-run to fill
# gaps. Banner stripping is REPORTED, not auto-applied (auto-deleting a CLAUDE.md would
# remove legit upstream guides like Saleor's) — strip flagged banners by hand from BOTH
# arms, keeping the two arms identical.
#
# Does NOT: pin SHAs (do that in PINNED_COMMITS.json first — ls-remote fills a missing
# commit if the url is present), run `sense setup` (the bench runner adds the sense
# steering at run time), or touch docker / freeze the held-out anchor — a vertical runs
# the two arms directly on the host; docker + freeze-heldout.sh are GLOBAL-bench only.
#
# Usage:
#   bash bench/drivers/provision-repos.sh                 # all repos in the vertical's repos.txt
#   bash bench/drivers/provision-repos.sh django haystack # a subset
#   bash bench/drivers/provision-repos.sh --index django  # also build the sense-arm index
#   bash bench/drivers/provision-repos.sh --check         # report state only, clone nothing
#
# Env: VERTICAL (default python-django), SENSE_CLONES (sense-arm root; baseline arm is its
#      sibling baseline/).
set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$BENCH_DIR/.."
PINNED="$BENCH_DIR/PINNED_COMMITS.json"
VERTICAL="${VERTICAL:-python-django}"
SENSE_ARM="${SENSE_CLONES:-$HOME/Developer/luuuc/oss/sense-benchmark/sense}"
BASE_ARM="$(dirname "$SENSE_ARM")/baseline"
REPOS_TXT="$BENCH_DIR/verticals/$VERTICAL/repos.txt"

DO_INDEX=0; CHECK=0; REPOS=()
for a in "$@"; do
  case "$a" in
    --index) DO_INDEX=1 ;;
    --check) CHECK=1 ;;
    -h|--help) sed -n '2,28p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $a" >&2; exit 64 ;;
    *) REPOS+=("$a") ;;
  esac
done
if [ "${#REPOS[@]}" -eq 0 ]; then
  [ -f "$REPOS_TXT" ] || { echo "no repos given and $REPOS_TXT missing" >&2; exit 2; }
  while IFS= read -r line; do
    line="${line%%#*}"; line="$(printf '%s' "$line" | tr -d '[:space:]')"
    [ -n "$line" ] && REPOS+=("$line")
  done < "$REPOS_TXT"
fi

pj() { python3 -c "import json,sys;d=json.load(open('$PINNED'));print((d.get('$1') or {}).get('$2',''))" 2>/dev/null; }
pset_commit() { python3 -c "import json;p='$PINNED';d=json.load(open(p));d.setdefault('$1',{})['commit']='$2';json.dump(d,open(p,'w'),indent=2,sort_keys=True);open(p,'a').write('\n')"; }

# shallow-fetch <sha> of <url> into <dir>, replacing any non-matching checkout
fetch_arm() { # url sha dir
  local url="$1" sha="$2" dir="$3"
  if [ -d "$dir/.git" ] && [ "$(git -C "$dir" rev-parse HEAD 2>/dev/null)" = "$sha" ]; then
    echo "    [ok]   ${dir/#$HOME/~} @ $sha"; return 0
  fi
  [ "$CHECK" = 1 ] && { echo "    [MISS] ${dir/#$HOME/~} (would fetch $sha)"; return 1; }
  rm -rf "$dir"; mkdir -p "$dir"
  ( cd "$dir" && git init -q && git remote add origin "$url" \
    && git fetch -q --depth 1 origin "$sha" && git checkout -q FETCH_HEAD ) \
    || { echo "    [FAIL] fetch $url @ $sha -> $dir"; return 1; }
  echo "    [clone] ${dir/#$HOME/~} @ $sha (depth 1)"
}

# scan a clone for an anti-LLM refusal banner (report; do not auto-strip)
banner_scan() { # dir
  local dir="$1" hit=0 f
  for f in CLAUDE.md AGENTS.md .cursorrules .cursor/rules .github/copilot-instructions.md; do
    [ -f "$dir/$f" ] || continue
    if grep -qiE "do not (use|train)|no (ai|llm|generative)|ai[- ]?(generated|assist).*(prohibit|forbid|ban)|anti[- ]?llm" "$dir/$f"; then
      echo "    [BANNER?] $f in ${dir/#$HOME/~} — review + strip from BOTH arms (lobsters rule)"; hit=1
    fi
  done
  return $hit
}

echo "== provisioning '$VERTICAL' — arms: $BASE_ARM + $SENSE_ARM =="
fail=0
for repo in "${REPOS[@]}"; do
  url="$(pj "$repo" url)"
  sha="$(pj "$repo" commit)"
  if [ -z "$url" ]; then echo "[$repo] no url in PINNED_COMMITS.json — add it first"; fail=1; continue; fi
  if [ -z "$sha" ]; then
    sha="$(GIT_TERMINAL_PROMPT=0 git ls-remote "$url" HEAD 2>/dev/null | awk '{print $1}')"
    [ -z "$sha" ] && { echo "[$repo] ls-remote failed for $url"; fail=1; continue; }
    [ "$CHECK" = 1 ] || { pset_commit "$repo" "$sha"; echo "[$repo] pinned HEAD -> $sha"; }
  fi
  echo "[$repo] $sha"
  fetch_arm "$url" "$sha" "$BASE_ARM/$repo" || fail=1
  fetch_arm "$url" "$sha" "$SENSE_ARM/$repo" || fail=1
  banner_scan "$BASE_ARM/$repo" || true
  if [ "$DO_INDEX" = 1 ] && [ "$CHECK" != 1 ]; then
    echo "    [index] sense arm ..."
    bash "$BENCH_DIR/lib/ensure-index.sh" "$repo" || { echo "    [index FAIL] $repo"; fail=1; }
  fi
done

echo ""
echo "== provisioning done (fail=$fail) =="
[ "$DO_INDEX" != 1 ] && echo "Next: index the sense arm — bash bench/lib/ensure-index.sh <repo> (gate Sentry; it dominates)."
exit $fail
