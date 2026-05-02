#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="$BENCH_DIR/results"
LIB_DIR="$BENCH_DIR/lib"

timestamp() { date +%Y-%m-%dT%H:%M:%S; }
log() { echo "[$(timestamp)] $*" >&2; }

TMPDIR_RESCORE=$(mktemp -d)
trap 'rm -rf "$TMPDIR_RESCORE"' EXIT

RESCORE_FILE="$TMPDIR_RESCORE/rescore.tsv"
NEW_FILE="$TMPDIR_RESCORE/new.tsv"
INVALID_FILE="$TMPDIR_RESCORE/invalid.tsv"
ALL_SCORED_FILE="$TMPDIR_RESCORE/all_scored.tsv"
: > "$RESCORE_FILE"
: > "$NEW_FILE"
: > "$INVALID_FILE"
: > "$ALL_SCORED_FILE"

# ── Phase 1: Discover all transcripts ──────────────────────────────

ALL_TRANSCRIPTS=$(find "$RESULTS_DIR" -name transcript.json -type f | sort)
TOTAL=$(echo "$ALL_TRANSCRIPTS" | wc -l | tr -d ' ')
log "Found $TOTAL transcripts under $RESULTS_DIR"

if [ "$TOTAL" -eq 0 ]; then
  log "Nothing to score."
  exit 0
fi

# ── Helpers ─────────────────────────────────────────────────────────

extract_path_parts() {
  local rel="${1#$RESULTS_DIR/}"
  TOOL=$(echo "$rel" | cut -d/ -f1)
  REPO=$(echo "$rel" | cut -d/ -f2)
  TASK=$(echo "$rel" | cut -d/ -f3)
  local segment4=$(echo "$rel" | cut -d/ -f4)
  RUN=""
  case "$segment4" in
    run-*) RUN="$segment4" ;;
  esac
}

get_f1() {
  python3 -c "
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    c = d.get('correctness', {})
    t = c.get('type', '')
    if t == 'set_match':
        print(c.get('f1', ''))
    elif t == 'keyword_presence':
        print(c.get('score', ''))
    else:
        print('')
except Exception:
    print('')
" "$1" 2>/dev/null
}

check_invalid() {
  python3 -c "
import json, sys
path = sys.argv[1]
reasons = []
try:
    with open(path) as f:
        lines = [l.strip() for l in f if l.strip()]
    if not lines:
        print('empty file'); sys.exit(0)
    has_result = False
    total_output = 0
    for line in lines:
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if obj.get('type') == 'result':
            has_result = True
            usage = obj.get('usage', {})
            total_output = usage.get('output_tokens', 0)
    if not has_result:
        reasons.append('no result event')
    if total_output == 0:
        reasons.append('zero output tokens')
    print('; '.join(reasons))
except Exception as e:
    print('parse error: %s' % e)
" "$1" 2>/dev/null
}

# ── Phase 2: Score each transcript, capturing before/after ─────────

scored=0
errors=0
skipped=0

# Piped while-loop runs in a subshell; counters are persisted via $TMPDIR_RESCORE/counts
echo "$ALL_TRANSCRIPTS" | while IFS= read -r transcript; do
  [ -z "$transcript" ] && continue
  dir="$(dirname "$transcript")"
  extract_path_parts "$transcript"
  label="tool=$TOOL repo=$REPO task=$TASK${RUN:+ $RUN}"

  invalid_reason=$(check_invalid "$transcript")
  if [ -n "$invalid_reason" ]; then
    printf '%s\t%s\t%s\t%s\t%s\n' "$TOOL" "$REPO" "$TASK" "${RUN:--}" "$invalid_reason" >> "$INVALID_FILE"
    log "  INVALID: $label — $invalid_reason"
    skipped=$((skipped + 1))
    echo "$scored $errors $skipped" > "$TMPDIR_RESCORE/counts"
    continue
  fi

  old_f1=""
  had_score=false
  if [ -f "$dir/scored.json" ]; then
    old_f1=$(get_f1 "$dir/scored.json")
    had_score=true
  fi

  log "Scoring: $label"
  if python3 "$LIB_DIR/scorer.py" "$dir" "$BENCH_DIR" "$TOOL" "$REPO" "$TASK" > "$dir/scored.json.tmp" 2>"$dir/score.log"; then
    mv "$dir/scored.json.tmp" "$dir/scored.json"
    new_f1=$(get_f1 "$dir/scored.json")
    scored=$((scored + 1))

    printf '%s\t%s\t%s\t%s\t%s\n' "$TOOL" "$REPO" "$TASK" "${RUN:--}" "${new_f1:-}" >> "$ALL_SCORED_FILE"

    if $had_score && [ -n "$old_f1" ] && [ -n "$new_f1" ]; then
      delta=$(python3 -c "import sys; print(round(float(sys.argv[1]) - float(sys.argv[2]), 4))" "$new_f1" "$old_f1")
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$TOOL" "$REPO" "$TASK" "${RUN:--}" "$old_f1" "$new_f1" "$delta" >> "$RESCORE_FILE"
    else
      printf '%s\t%s\t%s\t%s\t%s\n' "$TOOL" "$REPO" "$TASK" "${RUN:--}" "${new_f1:-n/a}" >> "$NEW_FILE"
    fi
  else
    rm -f "$dir/scored.json.tmp"
    log "  ERROR: scoring failed (see $dir/score.log)"
    errors=$((errors + 1))
  fi
  echo "$scored $errors $skipped" > "$TMPDIR_RESCORE/counts"
done

if [ -f "$TMPDIR_RESCORE/counts" ]; then
  read scored errors skipped < "$TMPDIR_RESCORE/counts"
fi

# ── Phase 3: Write needs-rerun.txt ─────────────────────────────────

NEEDS_RERUN="$RESULTS_DIR/needs-rerun.txt"
if [ -s "$INVALID_FILE" ]; then
  python3 -c "
import sys
out = open(sys.argv[2], 'w')
for line in open(sys.argv[1]):
    parts = line.rstrip('\n').split('\t')
    tool, repo, task, run, reason = parts[0], parts[1], parts[2], parts[3], parts[4]
    path = '/'.join([tool, repo, task] + ([run] if run != '-' else []))
    out.write('%s  # %s\n' % (path, reason))
out.close()
" "$INVALID_FILE" "$NEEDS_RERUN"
  invalid_count=$(wc -l < "$INVALID_FILE" | tr -d ' ')
  log "Wrote $invalid_count entries to $NEEDS_RERUN"
else
  rm -f "$NEEDS_RERUN"
fi

# ── Phase 4: Summary report (Python reads the temp TSV files) ──────

python3 -c "
import sys, os, statistics

rescore_path = sys.argv[1]
new_path = sys.argv[2]
invalid_path = sys.argv[3]
all_scored_path = sys.argv[4]
total = int(sys.argv[5])
scored_count = int(sys.argv[6])
error_count = int(sys.argv[7])
skipped_count = int(sys.argv[8])

def read_tsv(path):
    rows = []
    if os.path.exists(path) and os.path.getsize(path) > 0:
        with open(path) as f:
            for line in f:
                rows.append(line.rstrip('\n').split('\t'))
    return rows

rescore = read_tsv(rescore_path)
new = read_tsv(new_path)
invalid = read_tsv(invalid_path)
all_scored = read_tsv(all_scored_path)

# Multi-run variance: group by tool|repo|task for run-N entries
run_groups = {}
for row in all_scored:
    tool, repo, task, run, f1_str = row[0], row[1], row[2], row[3], row[4]
    if run == '-' or not f1_str:
        continue
    key = (tool, repo, task)
    run_groups.setdefault(key, []).append(float(f1_str))

variance = {}
for key, scores in run_groups.items():
    if len(scores) >= 2:
        variance[key] = {
            'scores': scores,
            'mean': statistics.mean(scores),
            'stddev': statistics.stdev(scores),
            'n': len(scores),
        }

print()
print('=' * 72)
print('RESCORE PIPELINE — SUMMARY')
print('=' * 72)
print()
print(f'  Total transcripts found:  {total}')
print(f'  Scored (new + re-scored): {scored_count}')
print(f'  Invalid / skipped:        {skipped_count}')
print(f'  Errors:                   {error_count}')
print()

# Table 1: Re-scored with delta
if rescore:
    rescore.sort(key=lambda r: abs(float(r[6])) if len(r) > 6 and r[6] else 0, reverse=True)
    print('─' * 72)
    print('RE-SCORED TRANSCRIPTS (sorted by |delta|)')
    print('─' * 72)
    hdr = f'{\"Tool\":<25} {\"Repo\":<14} {\"Task\":<18} {\"Old F1\":>7} {\"New F1\":>7} {\"Delta\":>7}'
    print(hdr)
    print(f'{\"─\"*25} {\"─\"*14} {\"─\"*18} {\"─\"*7} {\"─\"*7} {\"─\"*7}')
    for r in rescore:
        tool, repo, task, run = r[0], r[1], r[2], r[3]
        old, new_f1, delta = r[4], r[5], r[6]
        label = f'{tool}/{run}' if run and run != '-' else tool
        d = float(delta)
        sign = '+' if d > 0 else ''
        print(f'{label:<25} {repo:<14} {task:<18} {old:>7} {new_f1:>7} {sign}{delta:>6}')
    print()

# Table 2: Newly scored
if new:
    print('─' * 72)
    print('NEWLY SCORED TRANSCRIPTS')
    print('─' * 72)
    print(f'{\"Tool\":<25} {\"Repo\":<14} {\"Task\":<18} {\"F1\":>7}')
    print(f'{\"─\"*25} {\"─\"*14} {\"─\"*18} {\"─\"*7}')
    for r in new:
        tool, repo, task, run, f1 = r[0], r[1], r[2], r[3], r[4]
        label = f'{tool}/{run}' if run and run != '-' else tool
        print(f'{label:<25} {repo:<14} {task:<18} {f1:>7}')
    print()

# Table 3: Per-tool aggregate
tool_f1s = {}
for row in all_scored:
    tool, f1_str = row[0], row[4]
    try:
        tool_f1s.setdefault(tool, []).append(float(f1_str))
    except (ValueError, TypeError, IndexError):
        continue

if tool_f1s:
    print('─' * 72)
    print('PER-TOOL AGGREGATE')
    print('─' * 72)
    print(f'{\"Tool\":<25} {\"Count\":>6} {\"Mean F1\":>8} {\"Min\":>7} {\"Max\":>7}')
    print(f'{\"─\"*25} {\"─\"*6} {\"─\"*8} {\"─\"*7} {\"─\"*7}')
    for tool in sorted(tool_f1s.keys()):
        vals = tool_f1s[tool]
        mean = statistics.mean(vals) if vals else 0
        print(f'{tool:<25} {len(vals):>6} {mean:>8.4f} {min(vals):>7.4f} {max(vals):>7.4f}')
    print()

# Table 4: Invalid / flagged
if invalid:
    print('─' * 72)
    print('FLAGGED INVALID TRANSCRIPTS')
    print('─' * 72)
    print(f'{\"Tool\":<20} {\"Repo\":<14} {\"Task\":<18} {\"Reason\"}')
    print(f'{\"─\"*20} {\"─\"*14} {\"─\"*18} {\"─\"*30}')
    for r in invalid:
        tool, repo, task, run = r[0], r[1], r[2], r[3]
        reason = r[4] if len(r) > 4 else '?'
        label = f'{tool}/{run}' if run and run != '-' else tool
        print(f'{label:<20} {repo:<14} {task:<18} {reason}')
    print()

# Table 5: Multi-run variance
if variance:
    print('─' * 72)
    print('MULTI-RUN VARIANCE')
    print('─' * 72)
    print(f'{\"Tool\":<25} {\"Repo\":<14} {\"Task\":<18} {\"Runs\":>5} {\"Mean\":>7} {\"StdDev\":>7}')
    print(f'{\"─\"*25} {\"─\"*14} {\"─\"*18} {\"─\"*5} {\"─\"*7} {\"─\"*7}')
    for key in sorted(variance.keys()):
        v = variance[key]
        tool, repo, task = key
        print(f'{tool:<25} {repo:<14} {task:<18} {v[\"n\"]:>5} {v[\"mean\"]:>7.4f} {v[\"stddev\"]:>7.4f}')
    print()

print('=' * 72)
" "$RESCORE_FILE" "$NEW_FILE" "$INVALID_FILE" "$ALL_SCORED_FILE" \
  "$TOTAL" "$scored" "$errors" "$skipped"

log ""
log "=== Rescore complete ==="
log "  Scored: $scored | Errors: $errors | Skipped: $skipped | Total: $TOTAL"
