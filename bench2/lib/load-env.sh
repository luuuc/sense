# Shared env loader for the bench2 toolchain.
#
# Sourced from bench2/run.sh, bench2/judge.sh, bench2/freeze-heldout.sh,
# and bench2/improvement-loop/improve-loop.sh. Maps BENCHMARK_ANTHROPIC_API_KEY
# (the "this key is allowed to be billed" marker that lives in .env) onto
# ANTHROPIC_API_KEY so child processes pick it up without further translation.
#
# Why a separate file: every bench2 caller of Claude needs the same source
# of truth for the key. Per pitch 20-07, this script is locked — the
# improvement loop must not touch it.
#
# Usage (callers):
#     # near the top of the script, before any `claude` / python child:
#     SENSE_BENCH2_DIR="$(cd "$(dirname "$0")" && pwd)"  # or wherever bench2/ is
#     # shellcheck disable=SC1091
#     source "$SENSE_BENCH2_DIR/lib/load-env.sh"
#
# Behaviour:
# - If $BENCH2_PROJECT_ROOT/.env exists, source it (so BENCHMARK_ANTHROPIC_API_KEY
#   becomes a real env var rather than a file-only secret).
# - If BENCHMARK_ANTHROPIC_API_KEY is set and ANTHROPIC_API_KEY is unset, copy
#   the value across. We never overwrite an already-set ANTHROPIC_API_KEY —
#   the operator might have a reason to inject their own.
# - On macOS we also surface BENCHMARK_ANTHROPIC_API_KEY when running under
#   `nohup`, since some shells strip env on detach.

# Resolve project root: caller may set BENCH2_PROJECT_ROOT explicitly, else
# walk up from this file's directory (lib/ → bench2/ → project root).
if [[ -z "${BENCH2_PROJECT_ROOT:-}" ]]; then
  _load_env_lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  BENCH2_PROJECT_ROOT="$(cd "$_load_env_lib_dir/../.." && pwd)"
  unset _load_env_lib_dir
fi

if [[ -f "$BENCH2_PROJECT_ROOT/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$BENCH2_PROJECT_ROOT/.env"
  set +a
fi

if [[ -n "${BENCHMARK_ANTHROPIC_API_KEY:-}" && -z "${ANTHROPIC_API_KEY:-}" ]]; then
  export ANTHROPIC_API_KEY="$BENCHMARK_ANTHROPIC_API_KEY"
fi
