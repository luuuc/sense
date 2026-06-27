#!/usr/bin/env bash
# One-off recovery: redo the 3 throttle/cap-killed Qwen cells surgically.
# Files the fresh result over the bad cell ONLY if the new run is valid.
set -uo pipefail
cd $HOME/Developer/luuuc/oss/sense
export VERTICAL=ruby-rails BENCH_MODEL=ollama-cloud/qwen3-coder-next
MODEL=ollama-cloud/qwen3-coder-next
ROOT="bench/verticals/ruby-rails/results/ollama-cloud_qwen3-coder-next"

# cell = "arm repo run maxsecs"
CELLS=(
  "sense lobsters run-1 900"
  "baseline chatwoot run-1 1100"
  "baseline mastodon run-1 1400"
)

valid_meta() {  # $1=run_meta.json -> 0 if clean
  local m="$1"
  [ -f "$m" ] || return 1
  grep -q '"opencode_exit_code": 0' "$m" || return 1
  grep -q 'stalled_midrun\|hard_cap_timeout\|provider_cap_error\|empty_final_answer\|opencode_session_failed' "$m" && return 1
  local ac; ac=$(grep -o '"answer_chars"[^,}]*' "$m" | grep -o '[0-9]*$')
  [ -n "$ac" ] && [ "$ac" -ge 4000 ] || return 1
  return 0
}

for cell in "${CELLS[@]}"; do
  read -r arm repo run maxsecs <<<"$cell"
  rd="$ROOT/$arm/$repo"
  echo "===== RECOVER $arm/$repo/$run (MAX_SECS=$maxsecs) ====="
  # clear any stale flat files at repo level before the run
  find "$rd" -maxdepth 1 -type f -delete 2>/dev/null
  OPENCODE_STALL_IDLE=300 OPENCODE_MAX_SECS="$maxsecs" \
    bash bench/drivers/opencode-run.sh --tool "$arm" --repo "$repo" --model "$MODEL"
  if valid_meta "$rd/run_meta.json"; then
    ac=$(grep -o '"answer_chars"[^,}]*' "$rd/run_meta.json" | grep -o '[0-9]*$')
    echo ">>> VALID (answer_chars=$ac) — filing into $run/"
    find "$rd" -maxdepth 1 -type f -exec mv -f {} "$rd/$run/" \;
  else
    echo ">>> *** STILL INVALID — leaving $run/ untouched, clearing flat files ***"
    find "$rd" -maxdepth 1 -type f -delete 2>/dev/null
  fi
done
echo "===== recovery pass done ====="
