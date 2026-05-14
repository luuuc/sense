#!/usr/bin/env bash
set -euo pipefail

# Bench2 Autonomous Improvement Loop
#
# Single loop with N iterations to convergence.
# Each iteration: run → score → analyze → Claude reviews → apply → validate
#
# Usage:
#   improve-loop.sh [--iterations N] [--reviewer-model MODEL] [--model MODEL] [--repo REPOS]
#
# Examples:
#   improve-loop.sh                                      # 3 iterations, Opus 4.7 reviewer
#   improve-loop.sh --iterations 5 --repo gin,flask      # 5 iterations, specific repos
#   improve-loop.sh --reviewer-model claude-sonnet-4-6    # Use Sonnet as reviewer

LOOP_DIR="$(cd "$(dirname "$0")" && pwd)"
BENCH_DIR="$(cd "$LOOP_DIR/.." && pwd)"
BENCH_PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
INSTRUCT_DIR="$LOOP_DIR/instructions"
# shellcheck disable=SC1091
source "$BENCH_DIR/lib/load-env.sh"

ITERATIONS=10           # raised from 3 — convergence may need more
REVIEWER_MODEL="claude-opus-4-7"
MODEL=""
REPO_FILTER=""
TOOL_FILTER="sense,baseline"
RUNS=1
DRY_RUN=false
MAX_COST_USD=10.0       # Conservative default — a full iter on the full bench surface
                        # is ~$22 (sessions $9-19 + judge $3-5 + audit $6 + reviewer $2),
                        # so the default forces the operator to opt into a real run via
                        # `--max-cost-usd 30` or similar. Use `--repo flask` to drop the
                        # iter cost to ~$3 for cheap smoke tests.
FIRST_ITER_PRIOR=22.0   # Empirical: 12 sessions × per-repo budgets ($1.00-$2.25)
                        # = ~$19.50 worst case; +judge/audit/reviewer brings iter-1 to
                        # ~$22. Set as the prediction so predict-halt fires honestly.

while [[ $# -gt 0 ]]; do
  case "$1" in
    --iterations) ITERATIONS="$2"; shift 2 ;;
    --reviewer-model) REVIEWER_MODEL="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    --repo) REPO_FILTER="$2"; shift 2 ;;
    --tool) TOOL_FILTER="$2"; shift 2 ;;
    --runs) RUNS="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    --max-cost-usd) MAX_COST_USD="$2"; shift 2 ;;
    --first-iter-prior) FIRST_ITER_PRIOR="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: improve-loop.sh [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --iterations N        Max iterations to attempt (default: 10)"
      echo "  --reviewer-model M    Claude model for transcript review (default: claude-opus-4-7)"
      echo "  --model MODEL         Claude model for scenario runs"
      echo "  --repo REPOS          Comma-separated repo filter"
      echo "  --tool TOOLS          Comma-separated tool filter (default: sense,baseline)"
      echo "  --runs N              Runs per scenario for variance (default: 1)"
      echo "  --max-cost-usd USD    Hard cumulative cost ceiling (default: 10 — forces conscious opt-in; real iter is ~\$22)"
      echo "  --first-iter-prior USD  Predicted cost of iter-1 used for the halt check (default: 22)"
      echo "  --dry-run             Show what would run without executing"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

echo "Starting improvement loop: $ITERATIONS iterations, reviewer=$REVIEWER_MODEL, max-cost=\$$MAX_COST_USD"
echo ""

# Halt-reason state. Each stop trigger sets HALT_REASON and breaks the
# iter loop; after the loop we emit bench-readiness.md citing the reason.
HALT_REASON="max_iterations"  # default if we exit the loop naturally

# Track consecutive-pass streaks for the two-iter halt triggers.
CONV_PASS_STREAK=0
WATCHDOG_SUSPECT_STREAK=0

on_sigint() {
  echo "" >&2
  echo "SIGINT received — halting cleanly." >&2
  HALT_REASON="sigint"
  # Bash doesn't propagate a custom HALT_REASON out of a trap directly,
  # so we write a marker file the post-loop block reads.
  echo "sigint" > "$LOOP_DIR/results/loop-1/.halt-reason" 2>/dev/null || true
  exit 130
}
trap on_sigint INT

PHASE_ARGS=()
[[ -n "$MODEL" ]] && PHASE_ARGS+=(--model "$MODEL")
[[ -n "$REPO_FILTER" ]] && PHASE_ARGS+=(--repo "$REPO_FILTER")
[[ -n "$TOOL_FILTER" ]] && PHASE_ARGS+=(--tool "$TOOL_FILTER")
$DRY_RUN && PHASE_ARGS+=(--dry-run)

HISTORY_FILE="$LOOP_DIR/results/loop-1/iteration-history.jsonl"
CREDIT_FLAG="$LOOP_DIR/results/loop-1/credit-exhausted.flag"
mkdir -p "$LOOP_DIR/results/loop-1"

# ── Held-out integrity gate (panic class) ──
# The bench's anchor is held-out.lock. If any file under bench/scenarios/
# held-out/ has been modified vs the lockfile, refuse to start — every
# downstream score is suspect until the held-out set is restored.
# Skipped when the lockfile doesn't exist yet (pre-Card-5 state).
if [[ -f "$BENCH_DIR/locked/held-out.lock" ]]; then
  python3 "$BENCH_DIR/lib/lock_check.py" check-held-out \
    && held_out_rc=0 || held_out_rc=$?
  if [[ $held_out_rc -eq 2 ]]; then
    echo "" >&2
    echo "HELD-OUT INTEGRITY FAILURE — refusing to run." >&2
    echo "See $BENCH_DIR/locked/held-out.lock and bench/end-goal.md." >&2
    python3 "$BENCH_DIR/lib/readiness.py" \
      --loop-dir "$LOOP_DIR/results" \
      --halt-reason held_out_mismatch \
      --held-out-dir "$BENCH_DIR/scenarios/held-out" || true
    exit 2
  fi
fi

# Baseline snapshot — captured once before any iteration runs so iter-1's
# watchdog has a "before" reference point. Skipped on dry-run and when
# ANTHROPIC_API_KEY is missing (the auditor stack is API-gated; the loop
# still runs without it).
BASELINE_SNAPSHOT="$LOOP_DIR/results/loop-1/baseline-snapshot.json"
if ! $DRY_RUN && [[ -n "${ANTHROPIC_API_KEY:-}" && ! -f "$BASELINE_SNAPSHOT" ]]; then
  echo "Capturing baseline snapshot for watchdog..."
  python3 "$BENCH_DIR/lib/audit_watchdog.py" snapshot \
    --results-root "$BENCH_DIR/results" \
    --out "$BASELINE_SNAPSHOT" || true
fi

for iter in $(seq 1 "$ITERATIONS"); do
  echo "========================================"
  echo "  Iteration $iter/$ITERATIONS"
  echo "========================================"
  echo ""

  ITER_DIR="$LOOP_DIR/results/loop-1-iter-${iter}"

  # ── Halt-before-overspend check ──
  # IMPORTANT: do this BEFORE `mkdir $ITER_DIR`. Otherwise an empty iter
  # directory gets created and readiness.py picks it as the "latest iter"
  # with no data inside, masking the real last iter's results.
  if ! $DRY_RUN; then
    python3 "$BENCH_DIR/lib/cost_tracker.py" predict \
      --loop-dir "$LOOP_DIR/results" \
      --max-cost-usd "$MAX_COST_USD" \
      --first-iter-prior "$FIRST_ITER_PRIOR" \
      && cost_rc=0 || cost_rc=$?
    if [[ $cost_rc -eq 10 ]]; then
      echo ""
      echo "HALT: cost ceiling reached. Loop stops cleanly before iter $iter starts." >&2
      break
    elif [[ $cost_rc -ne 0 ]]; then
      echo "WARN: cost_tracker predict rc=$cost_rc — continuing anyway." >&2
    fi
  fi

  mkdir -p "$ITER_DIR"

  # ── Phase 1: Run scenarios, score, analyze ──
  echo "--- Phase 1: Run & Analyze ---"
  bash "$LOOP_DIR/scripts/phases/phase1-run-analysis.sh" \
    --loop 1 \
    --runs "$RUNS" \
    "${PHASE_ARGS[@]}"

  if $DRY_RUN; then
    echo "[dry-run] Would invoke Claude reviewer and continue."
    continue
  fi

  # ── Claude reviewer: read transcripts, generate improvements.json ──
  echo ""
  echo "--- Transcript Review (model: $REVIEWER_MODEL) ---"

  HISTORY_SECTION=""
  if [[ -f "$HISTORY_FILE" && -s "$HISTORY_FILE" ]]; then
    HISTORY_SECTION="## Previous Iterations

The following iterations have already been attempted. Do NOT re-propose changes that caused regressions or were rolled back. Learn from what worked and what failed.

$(cat "$HISTORY_FILE")
"
  fi

  # ── Scenario-auditor hints from the prior iter (if any) ──
  AUDIT_HINTS_SECTION=""
  if [[ $iter -gt 1 ]]; then
    PREV_AUDIT_DIR="$LOOP_DIR/results/loop-1-iter-$((iter - 1))"
    AUDIT_FILES=("$PREV_AUDIT_DIR"/audit-scenarios.*.json)
    if [[ -f "${AUDIT_FILES[0]}" ]]; then
      AUDIT_HINTS_SECTION="## Auditor hints (from iter $((iter - 1)))

The scenario auditor proposed the following check changes for the prior
iteration's state. Treat each as a *hint*, not a command — adopt, refine,
or reject with rationale. Always cite transcript evidence in the
modification's \`evidence\` field, even when an auditor proposed it. The
Phase 3 rollback safety net still protects against bad proposals.

$(cat "${AUDIT_FILES[@]}" 2>/dev/null)
"
    fi
  fi

  REVIEW_PROMPT="$(cat <<PROMPT
Read the following instruction files, then analyze the bench transcripts and generate improvements.json.

## Instructions

$(cat "$INSTRUCT_DIR/LOOP-CONTEXT.md")

---

$(cat "$INSTRUCT_DIR/phase1-analysis-instruct.md")

---

$(cat "$INSTRUCT_DIR/phase2-improve-instruct.md")

## Current Scores

$(python3 "$BENCH_DIR/lib/reporter.py" "$BENCH_DIR/results" --format terminal 2>/dev/null)

${HISTORY_SECTION}${AUDIT_HINTS_SECTION}## Task

1. Read the sense and baseline transcripts for all 6 repos in $BENCH_DIR/results/
2. Write analysis-notes.md to $ITER_DIR/analysis-notes.md
3. Write improvements.json to $ITER_DIR/improvements.json

Output ONLY the improvements.json content to stdout when done.
PROMPT
)"

  claude -p "$REVIEW_PROMPT" \
    --model "$REVIEWER_MODEL" \
    --allowedTools "Read,Bash,Write" \
    > "$ITER_DIR/claude-review.log" 2>&1

  if [[ ! -f "$ITER_DIR/improvements.json" ]]; then
    echo "ERROR: Reviewer did not produce improvements.json" >&2
    echo "Check $ITER_DIR/ for partial output." >&2
    exit 1
  fi

  echo "  Improvements generated: $ITER_DIR/improvements.json"

  # ── Phase 2: Apply improvements ──
  echo ""
  echo "--- Phase 2: Apply Improvements ---"
  bash "$LOOP_DIR/scripts/phases/phase2-run-improve.sh" \
    --loop 1 \
    --iter "$iter"

  # ── Phase 3: Re-run, score, validate ──
  echo ""
  echo "--- Phase 3: Validate ---"
  PHASE3_EXIT=0
  bash "$LOOP_DIR/scripts/phases/phase3-run-validate.sh" \
    --loop 1 \
    --iter "$iter" \
    --runs "$RUNS" \
    "${PHASE_ARGS[@]}" || PHASE3_EXIT=$?

  ITER_OUTCOME="applied"
  if [[ $PHASE3_EXIT -eq 2 ]]; then
    ITER_OUTCOME="rolled_back"
    echo ""
    echo "  Regression detected in iteration $iter. Scenarios rolled back."
  elif [[ $PHASE3_EXIT -ne 0 ]]; then
    echo "ERROR: Phase 3 failed with exit code $PHASE3_EXIT" >&2
    exit $PHASE3_EXIT
  fi

  # ── Phase 4: Audit (score / scenario / watchdog) ──
  echo ""
  echo "--- Phase 4: Audit ---"
  if [[ -f "$CREDIT_FLAG" ]]; then
    echo "  SKIP: credit-exhausted flag present at $CREDIT_FLAG" >&2
    echo "  (delete the flag once API balance is restored; see pitch 20-07)" >&2
  else
    phase4_args=(--loop 1 --iter "$iter" --tool "$TOOL_FILTER")
    [[ -n "$REPO_FILTER" ]] && phase4_args+=(--repo "$REPO_FILTER")
    BENCH_JUDGE_CALLER="phase4-audit" \
      bash "$LOOP_DIR/scripts/phases/phase4-audit.sh" "${phase4_args[@]}" \
      && p4_rc=0 || p4_rc=$?
    if [[ $p4_rc -eq 42 ]]; then
      printf '%s\n' \
        "credit-exhausted at iter=$iter phase=4-audit" \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        > "$CREDIT_FLAG"
      echo "  CREDIT EXHAUSTED — stamped $CREDIT_FLAG; subsequent iters will skip Phase 4." >&2
    elif [[ $p4_rc -ne 0 ]]; then
      echo "  WARN: Phase 4 had errors (rc=$p4_rc); auditor outputs may be incomplete." >&2
    fi
  fi

  # ── Phase 4b: held-out re-score (criterion 4 input) ──
  # Re-judge the 6 frozen held-out transcripts against the current
  # rubric. Outputs iter-N/validation/held-out-scored.json which
  # convergence.py correlates against bench/scenarios/held-out/*.gold.json.
  # Skipped if the credit-exhausted flag is set (it'd just be more
  # blocked API calls).
  if ! $DRY_RUN && [[ ! -f "$CREDIT_FLAG" ]]; then
    echo ""
    echo "--- Phase 4b: held-out re-score ---"
    BENCH_JUDGE_CALLER="heldout_rescore" \
      python3 "$BENCH_DIR/lib/heldout_rescore.py" \
        --iter-dir "$ITER_DIR" \
      && rs_rc=0 || rs_rc=$?
    if [[ $rs_rc -eq 42 ]]; then
      printf '%s\n' \
        "credit-exhausted at iter=$iter phase=4b-heldout-rescore" \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        > "$CREDIT_FLAG"
      echo "  CREDIT EXHAUSTED during held-out re-score — stamped $CREDIT_FLAG" >&2
    elif [[ $rs_rc -ne 0 ]]; then
      echo "  WARN: held-out re-score rc=$rs_rc; criterion 4 will defer this iter" >&2
    fi
  fi

  # ── Cost accounting (always public API pricing; see end-goal.md) ──
  if ! $DRY_RUN; then
    python3 "$BENCH_DIR/lib/cost_tracker.py" update \
      --loop-dir "$LOOP_DIR/results" \
      --iter "$iter" \
      --bench-results-dir "$BENCH_DIR/results" || \
      echo "WARN: cost_tracker update failed for iter=$iter" >&2
  fi

  # ── Record iteration history ──
  python3 -c "
import json, os
iter_dir = '$ITER_DIR'
entry = {'iteration': $iter, 'outcome': '$ITER_OUTCOME'}
imp_path = os.path.join(iter_dir, 'improvements.json')
if os.path.exists(imp_path):
    with open(imp_path) as f:
        entry['improvements'] = json.load(f)
post_path = os.path.join(iter_dir, 'post-analysis.json')
if os.path.exists(post_path):
    with open(post_path) as f:
        post = json.load(f)
    entry['scores_after'] = {r: d['current_scores'] for r, d in post.get('repos', {}).items()}
reg_path = os.path.join(iter_dir, 'regression.json')
if os.path.exists(reg_path):
    with open(reg_path) as f:
        entry['regression_check'] = json.load(f)
print(json.dumps(entry))
" >> "$HISTORY_FILE"

  echo ""
  echo "Iteration $iter complete ($ITER_OUTCOME)."
  echo ""

  # ── Convergence evaluator (four-criterion, per pitch 20-07) ──
  PREV_DIR=""
  if [[ $iter -gt 1 ]]; then
    PREV_DIR="$LOOP_DIR/results/loop-1-iter-$((iter - 1))"
  fi

  conv_args=(--iter-dir "$ITER_DIR" --held-out-dir "$BENCH_DIR/scenarios/held-out")
  [[ -n "$PREV_DIR" && -d "$PREV_DIR" ]] && conv_args+=(--prev-iter-dir "$PREV_DIR")
  python3 "$BENCH_DIR/lib/convergence.py" "${conv_args[@]}" \
    && conv_rc=0 || conv_rc=$?

  # delta.md gets the same inputs — keep them in sync.
  delta_args=(--iter-dir "$ITER_DIR" --held-out-dir "$BENCH_DIR/scenarios/held-out" \
              --iter-label "Iter $iter")
  [[ -n "$PREV_DIR" && -d "$PREV_DIR" ]] && delta_args+=(--prev-iter-dir "$PREV_DIR")
  python3 "$BENCH_DIR/lib/delta_report.py" "${delta_args[@]}" \
    --output "$ITER_DIR/delta.md" || true

  # convergence.py exits 0 iff all_pass; track the two-iter streak.
  if [[ $conv_rc -eq 0 ]]; then
    CONV_PASS_STREAK=$((CONV_PASS_STREAK + 1))
  else
    CONV_PASS_STREAK=0
  fi
  echo "  Convergence streak: $CONV_PASS_STREAK/2 iterations all-pass"

  if [[ $CONV_PASS_STREAK -ge 2 ]]; then
    HALT_REASON="converged"
    echo "Converged — four criteria held for two consecutive iterations. Stopping."
    break
  fi

  # ── Watchdog suspect streak (2 consecutive suspect verdicts → halt) ──
  watchdog_suspect_now=$(
    python3 -c "
import glob, json
files = sorted(glob.glob('$ITER_DIR/audit-watchdog.*.json'))
suspect = False
for p in files:
    try:
        d = json.load(open(p))
    except Exception:
        continue
    if d.get('verdict') == 'suspect' or d.get('flagged_for_human_review'):
        suspect = True
        break
print('1' if suspect else '0')
" 2>/dev/null)
  if [[ "$watchdog_suspect_now" == "1" ]]; then
    WATCHDOG_SUSPECT_STREAK=$((WATCHDOG_SUSPECT_STREAK + 1))
  else
    WATCHDOG_SUSPECT_STREAK=0
  fi
  if [[ $WATCHDOG_SUSPECT_STREAK -ge 2 ]]; then
    HALT_REASON="watchdog_suspect"
    echo "Watchdog flagged 2 consecutive iters as suspect — halting." >&2
    break
  fi

  # ── Credit-exhausted: stamp set during Phase 4? Halt at iter boundary. ──
  if [[ -f "$CREDIT_FLAG" ]]; then
    HALT_REASON="credit_exhausted"
    echo "credit-exhausted.flag set — halting cleanly at iter boundary." >&2
    break
  fi
done

# Halt-reason override from SIGINT trap, if any
if [[ -f "$LOOP_DIR/results/loop-1/.halt-reason" ]]; then
  HALT_REASON=$(cat "$LOOP_DIR/results/loop-1/.halt-reason")
  rm -f "$LOOP_DIR/results/loop-1/.halt-reason"
fi

# Re-check cost ceiling — if predict() would halt, the actual reason is cost.
if [[ "$HALT_REASON" == "max_iterations" ]]; then
  python3 "$BENCH_DIR/lib/cost_tracker.py" predict \
    --loop-dir "$LOOP_DIR/results" \
    --max-cost-usd "$MAX_COST_USD" \
    --first-iter-prior "$FIRST_ITER_PRIOR" 2>/dev/null \
    || HALT_REASON="cost_ceiling"
fi

# Emit the readiness verdict — the loop's real deliverable.
echo ""
echo "--- Emitting bench-readiness.md (halt_reason=$HALT_REASON) ---"
python3 "$BENCH_DIR/lib/readiness.py" \
  --loop-dir "$LOOP_DIR/results" \
  --halt-reason "$HALT_REASON" \
  --held-out-dir "$BENCH_DIR/scenarios/held-out" || \
  echo "WARN: readiness.py failed; continuing to final scoring." >&2

echo ""
echo "========================================"
echo "  Loop complete after $iter iterations"
echo "========================================"

# Final score + reports
echo ""
echo "--- Scoring final results ---"
bash "$BENCH_DIR/score.sh"
echo ""
bash "$BENCH_DIR/report.sh" --md
echo ""
bash "$BENCH_DIR/report.sh" --json
echo ""
bash "$BENCH_DIR/report.sh"

# Freshness guard: report.md must be at least as new as the newest
# scored.json. A stale report after a successful loop means report.sh ran
# but failed silently — surface it loudly instead of letting the next loop
# inherit yesterday's numbers.
REPORT_MD="$BENCH_DIR/results/report.md"
if [[ ! -f "$REPORT_MD" ]]; then
  echo "ERROR: $REPORT_MD was not generated" >&2
  exit 1
fi
NEWEST_SCORE=$(find "$BENCH_DIR/results" -name 'scored.json' -type f -print0 \
  | xargs -0 stat -f '%m' 2>/dev/null | sort -nr | head -1)
REPORT_MTIME=$(stat -f '%m' "$REPORT_MD" 2>/dev/null)
if [[ -n "$NEWEST_SCORE" && -n "$REPORT_MTIME" && "$REPORT_MTIME" -lt "$NEWEST_SCORE" ]]; then
  echo "ERROR: report.md is older than the newest scored.json — report.sh did not refresh it." >&2
  exit 1
fi
