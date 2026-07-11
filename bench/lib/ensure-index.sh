#!/usr/bin/env bash
# ensure-index.sh — rebuild a repo's Sense index ONLY when the scan engine changed.
#
# A `sense scan -rebuild -embed` is expensive (discourse ~20min embed) and is only
# necessary when the code that PRODUCES the index changed — the scan engine
# (internal/scan, extract, resolve, index) + schema version + embedding model.
# Query-layer changes (mcpio, dead, mcp, CLI-only) do NOT change scan output, so an
# index built by an earlier such binary is still fresh and re-scanning is wasted.
#
# This computes a "scan fingerprint" = hash of the scan engine's source (resolved
# transitively via `go list -deps`, so no dependency is missed) + schema + embed
# model, records it per repo in bench/index-state.json after each rebuild, and skips
# the rebuild when the recorded fingerprint already matches the current binary.
#
# Usage:
#   bash bench/lib/ensure-index.sh <repo> [<repo> ...]   # ensure fresh (rebuild if stale)
#   bash bench/lib/ensure-index.sh --check <repo> ...     # report freshness, do NOT rebuild
#   bash bench/lib/ensure-index.sh --all                  # every repo already in the log
#   FORCE_REBUILD=1 bash bench/lib/ensure-index.sh <repo> # rebuild regardless
#
# Safe by construction: if anything the scan engine transitively imports changes,
# the fingerprint changes and the repo is rescanned. The dangerous failure mode
# (serving a stale-resolver index — the bug that cost the solidus re-bench this
# session) cannot happen unless a scan-relevant change lands OUTSIDE the Go deps of
# the scan packages, which the module graph rules out.

set -uo pipefail
cd "$(dirname "$0")/../.."                       # sense repo root (has go.mod)
SENSE_REPO="$(pwd)"
CLONES="${SENSE_CLONES:-$HOME/Developer/luuuc/oss/sense-benchmark/sense}"
STATE="bench/index-state.json"
SENSE_BIN="${SENSE_BIN:-$HOME/.local/bin/sense}"

CHECK=0; ALL=0; MARK=0; REPOS=()
for a in "$@"; do
  case "$a" in
    --check) CHECK=1 ;;
    --all)   ALL=1 ;;
    --mark)  MARK=1 ;;   # record current fingerprint for an already-fresh index, no rebuild
    *)       REPOS+=("$a") ;;
  esac
done

# --- current scan fingerprint -------------------------------------------------
# Source of every first-party package the scan engine transitively depends on,
# minus test files (tests don't affect scan output), hashed; plus schema + model.
scan_fingerprint() {
  local mod pkgs dirs srchash vline gate
  mod="$(go list -m 2>/dev/null)" || { echo "ERR_GOLIST"; return; }
  pkgs="$(go list -deps ./internal/scan ./internal/extract ./internal/resolve ./internal/index 2>/dev/null | grep "^${mod}/")"
  [ -z "$pkgs" ] && { echo "ERR_NOPKGS"; return; }
  dirs="$(go list -f '{{.Dir}}' $pkgs 2>/dev/null)"
  srchash="$(find $dirs -maxdepth 1 -name '*.go' ! -name '*_test.go' 2>/dev/null | sort | xargs cat 2>/dev/null | shasum -a 256 | cut -c1-16)"
  # Schema version + embedding model gate the index too, but the marketing version does NOT.
  # Take only the "(schema vN, embeddings: MODEL)" parenthetical from --version, dropping the
  # version number, so a release that doesn't touch the scan engine (same srchash + same
  # schema + same model) reuses every existing index instead of forcing a full re-embed.
  vline="$("$SENSE_BIN" --version 2>/dev/null | tr -d '\n')"
  gate="${vline#*(}"; gate="${gate%)}"   # falls back to the full string if there is no parenthetical
  echo "${srchash}|${gate}"
}

json_get() { # repo field  -> value or empty
  python3 -c "import json,sys
try: d=json.load(open('$STATE'))
except FileNotFoundError: d={}
print((d.get('$1') or {}).get('$2',''))" 2>/dev/null
}

json_set() { # repo  fingerprint symbols edges
  python3 -c "import json,os,sys
p='$STATE'
try: d=json.load(open(p))
except FileNotFoundError: d={}
d['$1']={'fingerprint':'''$2''','symbols':'$3','edges':'$4','sense_version':'''$(\"$SENSE_BIN\" --version 2>/dev/null | tr -d \"\n\")''','git_head':'$(git -C \"$SENSE_REPO\" rev-parse --short HEAD 2>/dev/null)','built_at':'$(date -u +%Y-%m-%dT%H:%M:%SZ)'}
json.dump(d,open(p,'w'),indent=2,sort_keys=True); open(p,'a').write('\n')"
}

# --- clone hygiene guard --------------------------------------------------
# An index built from a dirty clone silently changes conventions and steering:
# on 2026-07-11 untracked mistral *.rb files a past agent left in the llm.rb
# clone were indexed and faked a bench regression. Refuse to rebuild when the
# clone carries modified or untracked files the scanner would INDEX — i.e.
# source files, matched by the extension list below (keep in sync with the
# extractors' Extensions()/Exts in internal/extract). sense-setup artifacts
# (CLAUDE.md, .mcp.json, .cursorrules, ...) are config the scanner ignores;
# the one source file setup writes (.opencode/plugin/sense.js) is excluded by
# path. ALLOW_DIRTY=1 bypasses for a deliberate local experiment.
SRC_EXT_RE='\.(rb|rake|gemspec|erb|go|rs|ts|tsx|js|jsx|mjs|cjs|py|c|h|cpp|cc|cxx|hpp|hxx|cs|java|php|kt|kts|scala|sc)$'
dirty_source() { # clone-dir -> prints modified/untracked indexable source files
  git -C "$1" status --porcelain -uall 2>/dev/null \
    | grep -vE '^\?\? \.opencode/' \
    | sed 's/^...//' \
    | grep -E "$SRC_EXT_RE"
}

FP="$(scan_fingerprint)"
if [[ "$FP" == ERR_* ]]; then echo "[ensure-index] cannot compute fingerprint ($FP) — is this the sense repo with go installed?"; exit 1; fi
echo "[ensure-index] current scan fingerprint: $FP"

if [ "$ALL" = 1 ]; then
  # --all = every repo with an index on disk (the ground truth for staleness),
  # unioned with anything already logged (so a repo whose index was deleted still
  # reports "no index"). Ordered small-first by index.db byte size so cheap repos
  # finish before the giants block the queue. Size-on-disk is used (not recorded
  # edges) because it exists for EVERY repo without a prior rebuild; a missing
  # index sorts as size 0 (reported as "no index" early). Name breaks ties.
  REPOS=()
  while IFS= read -r line; do [ -n "$line" ] && REPOS+=("$line"); done < <(SENSE_CLONES="$CLONES" python3 -c "import json,os
try: d=json.load(open('$STATE'))
except FileNotFoundError: d={}
clones=os.environ['SENSE_CLONES']
on_disk={r for r in os.listdir(clones) if os.path.isfile(os.path.join(clones,r,'.sense','index.db'))} if os.path.isdir(clones) else set()
def size(r):
    try: return os.path.getsize(os.path.join(clones,r,'.sense','index.db'))
    except OSError: return 0
print('\n'.join(sorted(on_disk | set(d), key=lambda r: (size(r), r))))" 2>/dev/null)
fi
[ "${#REPOS[@]}" -eq 0 ] && { echo "usage: ensure-index.sh [--check|--all] <repo> ..."; exit 2; }

rc=0
for repo in "${REPOS[@]}"; do
  clone="$CLONES/$repo"
  idx="$clone/.sense/index.db"
  logged="$(json_get "$repo" fingerprint)"
  if [ "${FORCE_REBUILD:-0}" != 1 ] && [ -f "$idx" ] && [ "$logged" = "$FP" ]; then
    echo "[fresh ] $repo — index matches current scan engine, skipping rebuild (built $(json_get "$repo" built_at))"
    continue
  fi
  if [ "$MARK" = 1 ]; then
    if [ ! -f "$idx" ]; then echo "[skip  ] $repo — no index to mark"; rc=1; continue; fi
    json_set "$repo" "$FP" "" ""
    echo "[marked] $repo — recorded current fingerprint for the existing index (no rebuild)"
    continue
  fi
  reason="stale"; [ ! -f "$idx" ] && reason="no index"; [ "${FORCE_REBUILD:-0}" = 1 ] && reason="forced"
  if [ "$CHECK" = 1 ]; then
    echo "[STALE ] $repo — would rebuild ($reason; logged=${logged:-none})"
    rc=1; continue
  fi
  if [ "${ALLOW_DIRTY:-0}" != 1 ]; then
    dirty="$(dirty_source "$clone")"
    if [ -n "$dirty" ]; then
      echo "[DIRTY ] $repo — clone has modified/untracked source files; refusing to index them:"
      echo "$dirty" | sed 's/^/    /'
      echo "          quarantine or 'git checkout/clean' them, or ALLOW_DIRTY=1 to override"
      rc=1; continue
    fi
  fi
  echo "[rebuild] $repo ($reason) ..."
  out="$(cd "$clone" && "$SENSE_BIN" scan -rebuild -embed 2>&1)"; ec=$?
  echo "$out" | grep -E "scanned|edges:" | sed 's/^/    /'
  if [ $ec -ne 0 ]; then echo "[FAIL  ] $repo rebuild exit=$ec"; rc=1; continue; fi
  syms="$(echo "$out" | grep -oE '[0-9]+ indexed' | grep -oE '[0-9]+' | head -1)"
  edges="$(echo "$out" | grep -oE '[0-9]+ resolved' | grep -oE '[0-9]+' | head -1)"
  json_set "$repo" "$FP" "$syms" "$edges"
  echo "[done  ] $repo — logged fingerprint (symbols=$syms edges=$edges)"
done
exit $rc
