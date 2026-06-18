#!/usr/bin/env bash
# sweep-rails.sh — run the Rails-vertical model sweep: each repo x each model,
# both arms (baseline,sense) on the subscription, each written to its own
# model-scoped root (results/vertical/ruby-rails/<model>/) so models never
# overwrite each other; the cross-model matrix is refreshed at the end.
#
# Idempotent: skips any (repo, model) already benched, so re-running after a
# rate-limit failure only fills the gaps. Resilient: a failed run is logged and
# the sweep continues. Judge stays claude-opus-4-7 (set in judge.py).
#
#   bash bench/sweep-rails.sh
#   MODELS="claude-opus-4-8" REPOS="ruby_llm" bash bench/sweep-rails.sh   # subset
#   MODELS="deepseek-v4-pro:cloud" REPOS="discourse" bash bench/sweep-rails.sh
#     # ollama-cloud ids route to opencode-run.sh (native ollama-cloud provider)
#   MODELS="ollama-cloud/kimi-k2.7-code ollama-cloud/qwen3-coder:480b" bash bench/sweep-rails.sh
#     # other ollama-cloud coding models (verified live via `opencode models`).
#     # Use the FULL provider/model id — the `:cloud` shorthand only works for ids
#     # without their own tag colon (deepseek-v4-pro:cloud). qwen3-coder-next is the
#     # newer alternative to qwen3-coder:480b. Cloud (not local) ids only.
#   MODELS="gpt-5.5 gpt-5.4" REPOS="discourse" bash bench/sweep-rails.sh
#     # gpt-*/codex: ids route to codex-run.sh (Codex CLI, ChatGPT subscription)
# Dispatch is by model id (see the case below); claude-* stays on the Claude CLI.

set -uo pipefail
BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$BENCH_DIR/.."
# Vertical wrapper: defaults to the ruby-rails vertical (baseline vs sense only),
# overridable with VERTICAL= (empty = global). Exports RESULTS_DIR/SCENARIOS_DIR so
# the runners write/read the model-scoped vertical subtree (the single source of
# truth; results are durable per model, so no separate .doc snapshot is kept).
VERTICAL="${VERTICAL-ruby-rails}"
source "$BENCH_DIR/lib/bench-paths.sh"

MODELS="${MODELS:-claude-opus-4-6 claude-opus-4-8}"
REPOS="${REPOS:-ruby_llm discourse}"
# For variance (--runs N) use runs-variance.sh, not this single-run sweeper.

echo "[sweep] models: $MODELS"
echo "[sweep] repos:  $REPOS"
for m in $MODELS; do
  # Re-resolve RESULTS_DIR to this model's own root so models never overwrite.
  unset RESULTS_DIR; export BENCH_MODEL="$m"; source "$BENCH_DIR/lib/bench-paths.sh"
  for r in $REPOS; do
    # Idempotent skip: this (model, repo) already has results under its model root.
    if [[ -f "$RESULTS_DIR/sense/$r/scored.json" || -d "$RESULTS_DIR/sense/$r/run-1" ]]; then
      echo "[skip] $r / $m (already benched)"
      continue
    fi
    echo "[run ] $r / $m  start $(date +%H:%M:%S)"
    rm -rf "$RESULTS_DIR/baseline/$r" "$RESULTS_DIR/sense/$r"
    # Dispatch the right harness by model id:
    #   ollama-cloud ids (deepseek-v4-pro:cloud, ollama-cloud/…) -> opencode
    #     (the Claude-CLI-at-daemon path drove cloud models so poorly they
    #      ignored Sense; opencode's native provider + CLI channel replaces it)
    #   codex ids (gpt-5.x, or a codex: prefix) -> codex
    #   everything else (claude-*) -> the subscription Claude runner
    case "$m" in
      *:cloud|ollama-cloud/*|ollama/*)
        run=(bash bench/opencode-run.sh --tool baseline,sense --repo "$r" --model "$m") ;;
      codex:*)
        run=(bash bench/codex-run.sh --tool baseline,sense --repo "$r" --model "${m#codex:}") ;;
      gpt-*|o3*|o4*)
        run=(bash bench/codex-run.sh --tool baseline,sense --repo "$r" --model "$m") ;;
      *)
        run=(bash bench/bench-sense-local.sh --tool baseline,sense --repo "$r" --no-build --model "$m") ;;
    esac
    if "${run[@]}"; then
      echo "[ok  ] $r / $m  done  $(date +%H:%M:%S)"
    else
      echo "[FAIL run] $r / $m  (rerun later — sweep is idempotent)"
    fi
  done
done
echo "[sweep] complete $(date +%H:%M:%S)"
# Refresh the vertical's cross-model matrix (results/vertical/<name>/report.md|json).
bash "$BENCH_DIR/report-matrix.sh" >/dev/null 2>&1 || echo "[warn] matrix refresh failed" >&2
