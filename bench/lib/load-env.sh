# Shared env loader for the bench toolchain.
#
# Sourced from bench/global/run.sh, bench/judge.sh, bench/global/freeze-heldout.sh.
#. Maps BENCHMARK_ANTHROPIC_API_KEY
# (the "this key is allowed to be billed" marker that lives in .env) onto
# ANTHROPIC_API_KEY so child processes pick it up without further translation.
#
# Why a separate file: every bench caller of Claude needs the same source
# of truth for the key. Per pitch 20-07, this script is locked — the
# improvement loop must not touch it.
#
# Usage (callers):
#     # near the top of the script, before any `claude` / python child:
#     SENSE_BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"  # or wherever bench/ is
#     # shellcheck disable=SC1091
#     source "$SENSE_BENCH_DIR/lib/load-env.sh"
#
# Behaviour:
# - If $BENCH_PROJECT_ROOT/.env exists, source it (so BENCHMARK_ANTHROPIC_API_KEY
#   becomes a real env var rather than a file-only secret).
# - If BENCHMARK_ANTHROPIC_API_KEY is set and ANTHROPIC_API_KEY is unset, copy
#   the value across. We never overwrite an already-set ANTHROPIC_API_KEY —
#   the operator might have a reason to inject their own.
# - On macOS we also surface BENCHMARK_ANTHROPIC_API_KEY when running under
#   `nohup`, since some shells strip env on detach.

# Resolve project root: caller may set BENCH_PROJECT_ROOT explicitly, else
# walk up from this file's directory (lib/ → bench/ → project root).
if [[ -z "${BENCH_PROJECT_ROOT:-}" ]]; then
  _load_env_lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  BENCH_PROJECT_ROOT="$(cd "$_load_env_lib_dir/../.." && pwd)"
  unset _load_env_lib_dir
fi

if [[ -f "$BENCH_PROJECT_ROOT/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$BENCH_PROJECT_ROOT/.env"
  set +a
fi

if [[ -n "${BENCHMARK_ANTHROPIC_API_KEY:-}" && -z "${ANTHROPIC_API_KEY:-}" ]]; then
  export ANTHROPIC_API_KEY="$BENCHMARK_ANTHROPIC_API_KEY"
fi
