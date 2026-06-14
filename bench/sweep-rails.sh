#!/usr/bin/env bash
# sweep-rails.sh — run the Rails-vertical model sweep: each repo x each model,
# both arms (baseline,sense) on the subscription, snapshotting each result into
# .doc/launch/02-rails-vertical/results/<repo>/<model>/ before the next run
# overwrites bench/results/.
#
# Idempotent: skips any (repo, model) already snapshotted, so re-running after a
# rate-limit failure only fills the gaps. Resilient: a failed run/snapshot is
# logged and the sweep continues. Judge stays claude-opus-4-7 (set in judge.py).
#
#   bash bench/sweep-rails.sh
#   MODELS="claude-fable-5" REPOS="ruby_llm" bash bench/sweep-rails.sh   # subset
#   MODELS="deepseek-v4-pro:cloud" REPOS="discourse" bash bench/sweep-rails.sh
#     # ollama-cloud ids route to opencode-run.sh (native ollama-cloud provider)
#   MODELS="gpt-5.5 gpt-5.4" REPOS="discourse" bash bench/sweep-rails.sh
#     # gpt-*/codex: ids route to codex-run.sh (Codex CLI, ChatGPT subscription)
# Dispatch is by model id (see the case below); claude-* stays on the Claude CLI.

set -uo pipefail
cd "$(dirname "$0")/.."

MODELS="${MODELS:-claude-opus-4-6 claude-opus-4-7 claude-opus-4-8 claude-fable-5}"
REPOS="${REPOS:-ruby_llm discourse}"
DOC=".doc/launch/02-rails-vertical/results"
# For variance (--runs N) use runs-variance.sh, not this single-run sweeper.

echo "[sweep] models: $MODELS"
echo "[sweep] repos:  $REPOS"
for m in $MODELS; do
  for r in $REPOS; do
    # snapshot-result.sh sanitizes / and : to _ in the model dir name; match it
    # here so the idempotent skip works for cloud ids like deepseek-v4-pro:cloud.
    msan="${m//[\/:]/_}"
    if [[ -f "$DOC/$r/$msan/summary.md" ]]; then
      echo "[skip] $r / $m (already snapshotted)"
      continue
    fi
    echo "[run ] $r / $m  start $(date +%H:%M:%S)"
    rm -rf "bench/results/baseline/$r" "bench/results/sense/$r"
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
      bash bench/lib/snapshot-result.sh "$r" || echo "[FAIL snapshot] $r / $m"
      echo "[ok  ] $r / $m  done  $(date +%H:%M:%S)"
    else
      echo "[FAIL run] $r / $m  (rerun later — sweep is idempotent)"
    fi
  done
done
echo "[sweep] complete $(date +%H:%M:%S)"
