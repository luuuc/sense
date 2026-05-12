#!/usr/bin/env bash
set -euo pipefail

# Phase 2: Apply improvements → Validate → Prepare for re-run
# Takes improvements.json (generated during analysis) and applies to scenarios

LOOP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
BENCH2_DIR="$(cd "$LOOP_DIR/.." && pwd)"
TOOLS_DIR="$LOOP_DIR/scripts/tools"

LOOP=1
ITER=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    --loop) LOOP="$2"; shift 2 ;;
    --iter) ITER="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

ITER_DIR="$LOOP_DIR/results/loop-${LOOP}-iter-${ITER}"
mkdir -p "$ITER_DIR/improved-scenarios"

IMPROVEMENTS="$ITER_DIR/improvements.json"

if [[ ! -f "$IMPROVEMENTS" ]]; then
  echo "ERROR: $IMPROVEMENTS not found." >&2
  echo "Generate it during analysis, then re-run this phase." >&2
  exit 1
fi

echo "=== Phase 2: Apply Improvements (Loop $LOOP, Iter $ITER) ==="

# Step 1: Generate improved scenario YAMLs
echo "  Applying improvements..."
python3 "$TOOLS_DIR/generate-improvements.py" \
  --improvements "$IMPROVEMENTS" \
  --scenarios-dir "$BENCH2_DIR/scenarios" \
  --output-dir "$ITER_DIR/improved-scenarios" \
  > "$ITER_DIR/changes-manifest.json"

# Step 2: Pre-run validation
echo "  Validating..."
python3 "$TOOLS_DIR/validate-changes.py" \
  --original-dir "$BENCH2_DIR/scenarios" \
  --improved-dir "$ITER_DIR/improved-scenarios" \
  --backup-dir "$ITER_DIR/backups" \
  --output "$ITER_DIR/validation.json"

VALID=$(python3 -c "import json; print(json.load(open('$ITER_DIR/validation.json'))['valid'])")
if [[ "$VALID" != "True" ]]; then
  echo "  VALIDATION FAILED. See $ITER_DIR/validation.json" >&2
  cat "$ITER_DIR/validation.json" >&2
  exit 1
fi

# Step 3: Apply improved scenarios
echo "  Copying improved scenarios to $BENCH2_DIR/scenarios/"
cp "$ITER_DIR/improved-scenarios"/*.yaml "$BENCH2_DIR/scenarios/"

echo ""
echo "Phase 2 complete. Scenarios updated."
echo "  Backup at: $ITER_DIR/backups/"
echo "  Changes:   $ITER_DIR/changes-manifest.json"
echo ""
echo "Next: Re-run scenarios and validate with Phase 3."
