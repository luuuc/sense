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

FP="$(scan_fingerprint)"
if [[ "$FP" == ERR_* ]]; then echo "[ensure-index] cannot compute fingerprint ($FP) — is this the sense repo with go installed?"; exit 1; fi
echo "[ensure-index] current scan fingerprint: $FP"

if [ "$ALL" = 1 ]; then
  mapfile -t REPOS < <(python3 -c "import json;print('\n'.join(json.load(open('$STATE')).keys()))" 2>/dev/null)
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
