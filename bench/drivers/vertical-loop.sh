#!/usr/bin/env bash
# vertical-loop.sh — the mechanical per-repo loop of the vertical bench (manifesto
# §8, bootstrap-runbook §6), with the two human gates kept load-bearing.
#
# It chains the FREE/mechanical steps and PAUSES where someone must act. The line
# auto-improvement-endgame draws: Loop A (the product-gap detectors) and the run
# mechanics are automated; Loop B (the scenario + the tie diagnosis) is DRAFTED and
# tuned by the AI agent running this loop, and the human REVIEWS it adversarially,
# asynchronously (anomalies / inconsistencies). The human is the integrity anchor,
# not the author. The script cannot write a scenario itself, so at the scout pause it
# hands control back to the AI agent to author <repo>.yaml + <repo>.rubric.yaml +
# gold, then resumes once those exist. The only true HUMAN decision is the cost
# confirm before the paid sweep (--yes).
#
# Phases (a state file resumes at the next one; re-run to advance):
#   index      ensure-index.sh                                    [auto]
#   scout      seam_hunt.py --propose  ->  GATE: draft+review scenario+gold
#   preflight  render prompt + loopA preflight (resolve_oracle)   [auto] -> cost GATE
#   bench      runs-variance.sh Opus x2 (PAID)                    [GATE: --yes]
#   report     pergroup.py verdict; WIN -> harvest, else          GATE: diagnose
#   harvest    loopA-scan.sh harvest (mine the paid transcripts)  [auto] -> done
#
# Usage:
#   bash bench/drivers/vertical-loop.sh <repo>                 # run from saved phase
#   bash bench/drivers/vertical-loop.sh <repo> --symbol ProductVariant --file product/models.py
#   bash bench/drivers/vertical-loop.sh <repo> --yes          # confirm the paid bench
#   bash bench/drivers/vertical-loop.sh <repo> --phase scout  # force one phase
#   bash bench/drivers/vertical-loop.sh <repo> --reset        # back to index
#   bash bench/drivers/vertical-loop.sh --status              # all repos' phases
#
# Env: VERTICAL (default python-django), MODELS (default claude-opus-4-8),
#      RUNS (default 2), SENSE_CLONES (default ~/Developer/luuuc/oss/sense-benchmark/sense).
set -uo pipefail

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$BENCH_DIR/.."
LIB="$BENCH_DIR/lib"
VERTICAL="${VERTICAL:-python-django}"
MODELS="${MODELS:-claude-opus-4-8}"
HEADLINE_MODEL="${MODELS%% *}"      # first model = the headline arm the verdict reads
RUNS="${RUNS:-2}"
CLONES="${SENSE_CLONES:-$HOME/Developer/luuuc/oss/sense-benchmark/sense}"
SCEN_DIR="$BENCH_DIR/verticals/$VERTICAL/scenarios"
STATE="$BENCH_DIR/verticals/$VERTICAL/.loop-state.json"
PHASES=(index scout preflight bench report harvest done)

# ---- args -------------------------------------------------------------------
REPO=""; SYMBOL=""; FILE_HINT=""; YES=0; FORCE_PHASE=""; STATUS=0; RESET=0
while [ $# -gt 0 ]; do
  case "$1" in
    --symbol) SYMBOL="$2"; shift 2 ;;
    --file)   FILE_HINT="$2"; shift 2 ;;
    --phase)  FORCE_PHASE="$2"; shift 2 ;;
    --yes|-y) YES=1; shift ;;
    --status) STATUS=1; shift ;;
    --reset)  RESET=1; shift ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; exit 64 ;;
    *)  REPO="$1"; shift ;;
  esac
done

# ---- state helpers (JSON map repo -> phase) ---------------------------------
state_get() { # repo -> phase ("" if unknown)
  python3 -c "import json,sys
try: d=json.load(open('$STATE'))
except Exception: d={}
print(d.get('$1',''))"
}
state_set() { # repo phase
  python3 -c "import json,os
p='$STATE'
try: d=json.load(open(p))
except Exception: d={}
d['$1']='$2'
os.makedirs(os.path.dirname(p),exist_ok=True)
json.dump(d,open(p,'w'),indent=2,sort_keys=True); open(p,'a').write('\n')"
}
state_dump() {
  python3 -c "import json
try: d=json.load(open('$STATE'))
except Exception: d={}
print('\n'.join(f'  {k:16} {v}' for k,v in sorted(d.items())) or '  (none)')"
}

if [ "$STATUS" = 1 ] && [ -z "$REPO" ]; then
  echo "[$VERTICAL] phases:"; state_dump; exit 0
fi
[ -z "$REPO" ] && { echo "usage: vertical-loop.sh <repo> [--symbol S] [--file F] [--yes] [--phase P] [--reset]" >&2; exit 64; }

CLONE="$CLONES/$REPO"
YAML="$SCEN_DIR/$REPO.yaml"
RUBRIC="$SCEN_DIR/$REPO.rubric.yaml"

if [ "$STATUS" = 1 ]; then echo "[$VERTICAL/$REPO] phase: $(state_get "$REPO" || echo index)"; exit 0; fi
if [ "$RESET" = 1 ]; then state_set "$REPO" index; echo "[$VERTICAL/$REPO] reset to phase 'index'"; exit 0; fi

# yaml field reader (contract_symbol / contract_file), no yaml dep needed
yaml_field() { grep -E "^$1:" "$YAML" 2>/dev/null | head -1 | sed -E "s/^$1:[[:space:]]*//" | tr -d '"'"'"; }

gate() { # message... — record we're parked at the current phase and stop
  echo ""
  echo "==================== PAUSE — ACTION NEEDED ===================="
  printf '%s\n' "$@"
  echo "=============================================================="
  echo "Re-run: bash bench/drivers/vertical-loop.sh $REPO   (resumes at this phase)"
  exit 0
}

# ---- phase implementations (echo the next phase on success, or gate+exit) ----
do_index() {
  echo "## [index] ensure $REPO index matches the current scan engine"
  bash "$LIB/ensure-index.sh" "$REPO" || { echo "[index] FAILED — fix the index before continuing"; exit 1; }
  NEXT=scout
}

do_scout() {
  if [ -f "$YAML" ] && [ -f "$RUBRIC" ]; then
    echo "## [scout] scenario present ($REPO.yaml + .rubric.yaml) — advancing"
    NEXT=preflight; return
  fi
  echo "## [scout] propose the contract + candidate gold (advisory)"
  local sym="$SYMBOL"; [ -z "$sym" ] && sym="$(yaml_field contract_symbol)"
  local fh="$FILE_HINT"; [ -z "$fh" ] && fh="$(yaml_field contract_file)"
  if [ -n "$sym" ]; then
    local fflag=(); [ -n "$fh" ] && fflag=(--file "$fh")
    # ${arr[@]+...} guards the empty-array case under `set -u` on bash 3.2 (macOS).
    python3 "$LIB/seam_hunt.py" "$CLONE" "$sym" --conf 0.7 --propose ${fflag[@]+"${fflag[@]}"} 2>&1 || true
  else
    echo "  (no --symbol given and no scenario yet; pick the central abstraction from"
    echo "   repos.md, then: vertical-loop.sh $REPO --symbol <Sym> [--file <path>])"
  fi
  gate \
    "AI AUTHORING STEP (Loop B) — the agent running this loop DRAFTS + tunes these now;" \
    "the human reviews adversarially, async (anomalies / inconsistencies), not as the author:" \
    "  - $YAML            (7 neutral steps, audit step forces per-dep file:line)" \
    "  - $RUBRIC   (matching rubric)" \
    "  - gold: + contract_symbol:/contract_file: in the yaml (curate the candidate above)" \
    "Guidance: .doc/launch/02-rails-vertical/scenario-crafting.md + manifesto §4/§13." \
    "Scenarios are refined AFTER the first bench to hit the +0.50 floor — draft, don't perfect."
}

do_preflight() {
  echo "## [preflight] render prompt (leak check) + Loop-A resolve_oracle (\$0)"
  [ -f "$YAML" ] || { echo "[preflight] $YAML missing — back to scout"; state_set "$REPO" scout; exit 1; }
  echo "---- rendered agent prompt (verify it leaks NO paths/symbols/counts/tool names, manifesto §13) ----"
  python3 "$LIB/scenario.py" "$YAML" --prompt 2>&1 | sed 's/^/  /' || true
  echo "---- Loop-A preflight (gold must be default-blast-retrievable, manifesto §10) ----"
  bash "$LIB/loopA-scan.sh" preflight "$VERTICAL" "$REPO" 2>&1 || true
  if [ "$YES" = 1 ]; then
    echo "## [preflight] --yes given — proceeding to the paid bench"
    NEXT=bench; return
  fi
  gate \
    "COST GATE — the next phase spends real tokens (Opus 4.8 x$RUNS, both arms, ~\$10-18)." \
    "Confirm the prompt is leak-free and the oracle retrieves the gold, then run:" \
    "  bash bench/drivers/vertical-loop.sh $REPO --yes"
  # (gate exits; phase stays preflight. --yes re-enters here and advances to bench.)
}

do_bench() {
  if [ "$YES" != 1 ]; then
    if [ -t 0 ]; then
      read -r -p "## [bench] spend on Opus 4.8 x$RUNS both arms for $REPO? [y/N] " a
      [[ "$a" =~ ^[Yy]$ ]] || gate "Bench not confirmed. Re-run with --yes when ready."
    else
      gate "Bench step needs confirmation (non-interactive). Re-run with --yes."
    fi
  fi
  echo "## [bench] VERTICAL=$VERTICAL MODELS='$MODELS' RUNS=$RUNS runs-variance.sh $REPO"
  VERTICAL="$VERTICAL" MODELS="$MODELS" RUNS="$RUNS" \
    bash "$BENCH_DIR/drivers/runs-variance.sh" "$REPO" || { echo "[bench] FAILED"; exit 1; }
  NEXT=report
}

do_report() {
  echo "## [report] per-group cited-recall verdict (headline arm: $HEADLINE_MODEL)"
  # RESULTS_DIR mirrors bench-paths.sh: verticals/<name>/results/<sanitized-model>.
  local msan rdir out
  msan="$(printf '%s' "$HEADLINE_MODEL" | tr '/:' '__')"
  rdir="$BENCH_DIR/verticals/$VERTICAL/results/$msan"
  out="$(RESULTS_DIR="$rdir" python3 "$LIB/pergroup.py" "$REPO" 0.50 2>&1)"
  echo "$out" | sed 's/^/  /'
  if echo "$out" | grep -q '^VERDICT: WIN'; then
    echo "  -> WIN (discriminator >= +0.50). Banking Loop-A harvest."
    NEXT=harvest; return
  fi
  gate \
    "BELOW +0.50 — diagnose the tie (manifesto §8.8; the model drafts the fix, human reviews):" \
    "  - per-dep tally: which gold ids the baseline CITED vs MISSED across runs" \
    "  - \$0 gold-retarget: move baseline-gets-3/3 deps to 'context', promote baseline-misses-3/3" \
    "    re-score (no re-bench): python3 bench/lib/scorer.py <run_dir> $YAML bench" \
    "  - only re-author + re-bench if the PROMPT/steps must change" \
    "After retargeting/re-authoring, re-run; a gold-only change needs NO new bench."
}

do_harvest() {
  echo "## [harvest] Loop-A transcript_miss over the paid transcripts (\$0, advisory)"
  bash "$LIB/loopA-scan.sh" harvest "$VERTICAL" "$HEADLINE_MODEL" 2>&1 || true
  echo ""
  echo "## $REPO — Definition of Done (manifesto §14), confirm by hand:"
  echo "   [ ] discriminator >= +0.50 (favored +0.80) on Opus 4.8 x$RUNS"
  echo "   [ ] Sense adopted its tools (mcp_count>0), no hallucinated cites, baseline floor legit"
  echo "   [ ] efficiency reported, scenario human-readable + leak-free, article matches the numbers"
  NEXT=done
}

# ---- driver: run phases from the current one until a gate stops us ----------
PHASE="${FORCE_PHASE:-$(state_get "$REPO")}"; [ -z "$PHASE" ] && PHASE=index
echo "[$VERTICAL/$REPO] entering at phase '$PHASE' (models='$MODELS' runs=$RUNS)"

while :; do
  NEXT=""
  # Each phase prints its own progress and EITHER sets NEXT (advance) OR calls
  # gate()/exit (stop). Output flows straight to the terminal (no capture).
  case "$PHASE" in
    index)     do_index ;;
    scout)     do_scout ;;
    preflight) do_preflight ;;
    bench)     do_bench ;;
    report)    do_report ;;
    harvest)   do_harvest ;;
    done)      echo "[$VERTICAL/$REPO] phase 'done' — nothing to do (use --reset to rerun)"; exit 0 ;;
    *)         echo "unknown phase '$PHASE'" >&2; exit 64 ;;
  esac
  [ -z "$NEXT" ] && { echo "[$VERTICAL/$REPO] phase '$PHASE' set no next phase — stopping" >&2; exit 1; }
  state_set "$REPO" "$NEXT"
  [ -n "$FORCE_PHASE" ] && { echo "[$VERTICAL/$REPO] phase '$PHASE' done; next is '$NEXT' (--phase forced single step)"; exit 0; }
  PHASE="$NEXT"
  [ "$PHASE" = done ] && { echo "[$VERTICAL/$REPO] all phases complete."; exit 0; }
done
