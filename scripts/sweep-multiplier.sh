#!/usr/bin/env bash
# sweep-multiplier.sh — sweep Experimental.LevelMultiplier for the write benchmark.
#
# Same skeleton as sweep-lbase.sh, but the varying knob is `level_multiplier`
# instead of `l_base_max_bytes`. `l_base_max_bytes` is left at Pebble's default
# (64MB) so the multiplier is the only changing variable.
#
# Why this knob for our random-hash workload:
#   - L0→Lbase compactions are capped at 500MB regardless of L0CompactionThreshold,
#     so that knob doesn't move the needle on our config (256MB memtables already
#     hit the cap with the default 2-sublevel trigger).
#   - Total write amp ≈ 1 (flush) + (L0→Lbase WA) + Σ (multiplier per downstream level).
#     LevelMultiplier directly controls the per-level cost AND, by changing the
#     fan-out, the number of active levels for a given DB size.
#   - There's a sweet spot: too small → too many levels (more compactions in the
#     chain); too large → each compaction rewrites more "passenger" data per byte.
#
# Outputs per case (same layout as sweep-lbase.sh):
#   results/<run-id>/<case-name>.json
#   results/<run-id>/<case-name>.log
# Plus at the end:
#   results/<run-id>/compare-<baseline>-vs-<case>.{txt,md}
#   results/<run-id>/SUMMARY.md
set -euo pipefail

#───────────────────────────────────────────────────────────────────────────────
# Defaults — fill bench-dir-derived ones AFTER argument parsing.
#───────────────────────────────────────────────────────────────────────────────
BENCH_DIR="${BENCH_DIR:-/home/gary/bench-env/pebble-bench/pebblev1}"
PEBBLE_BENCH="${PEBBLE_BENCH:-}"
DURATION="${DURATION:-20m}"
CONCURRENCY="${CONCURRENCY:-}"
BENCHMARK="${BENCHMARK:-write}"
DATA_ROOT="${DATA_ROOT:-}"
RESULTS_ROOT="${RESULTS_ROOT:-}"
EXTRA_OVERRIDES=()
DRY_RUN=0

# 10 is Pebble's default — keep it as the baseline. The other rungs span the
# plausible window: below 5 the chain gets too long, above ~12 the per-level
# passenger cost grows fast.
CASES=(
  "mult-5:5"
  "mult-6:6"
  "mult-7:7"
  "mult-8:8"
  "mult-10:10"
)
BASELINE_INDEX=4   # mult-10 is the baseline (Pebble default)

usage() {
  cat <<EOF
Usage: $0 [options]

Sweeps Pebble's Experimental.LevelMultiplier (default 10) while keeping
LBaseMaxBytes at its default of 64MB.

Options:
  --bench-dir <path>      Working directory containing pebble-bench (default: ${BENCH_DIR})
  --binary <path>         Path to the pebble-bench binary (default: \$BENCH_DIR/pebble-bench)
  --duration <dur>        Per-case duration, e.g. 10m, 20m (default: ${DURATION})
  --concurrency <n>       --concurrency override forwarded to pebble-bench
  --benchmark <name>      Benchmark name (default: ${BENCHMARK})
  --data-root <path>      Where to place per-case data dirs (default: \$BENCH_DIR/sweep-data)
  --results-root <path>   Where to place per-case JSON/log/comparisons
  --override key=value    Extra --override forwarded to every case (repeatable)
  --dry-run               Print commands without running them
  -h, --help              Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bench-dir)    BENCH_DIR="$2"; shift 2 ;;
    --binary)       PEBBLE_BENCH="$2"; shift 2 ;;
    --duration)     DURATION="$2"; shift 2 ;;
    --concurrency)  CONCURRENCY="$2"; shift 2 ;;
    --benchmark)    BENCHMARK="$2"; shift 2 ;;
    --data-root)    DATA_ROOT="$2"; shift 2 ;;
    --results-root) RESULTS_ROOT="$2"; shift 2 ;;
    --override)     EXTRA_OVERRIDES+=("$2"); shift 2 ;;
    --dry-run)      DRY_RUN=1; shift ;;
    -h|--help)      usage; exit 0 ;;
    *) echo "unknown flag: $1" >&2; usage; exit 2 ;;
  esac
done

: "${PEBBLE_BENCH:=${BENCH_DIR}/pebble-bench}"
: "${DATA_ROOT:=${BENCH_DIR}/sweep-data}"
: "${RESULTS_ROOT:=${BENCH_DIR}/sweep-results}"

if [[ ! -x "$PEBBLE_BENCH" ]]; then
  echo "pebble-bench binary not found or not executable: $PEBBLE_BENCH" >&2
  exit 1
fi

RUN_ID="$(date +%Y%m%d-%H%M%S)-mult"
RUN_DIR="${RESULTS_ROOT}/${RUN_ID}"
mkdir -p "$RUN_DIR"

{
  echo "# Sweep metadata"
  echo "run_id:        ${RUN_ID}"
  echo "started:       $(date -Iseconds)"
  echo "host:          $(hostname)"
  echo "bench_dir:     ${BENCH_DIR}"
  echo "binary:        ${PEBBLE_BENCH}"
  echo "duration:      ${DURATION}"
  echo "concurrency:   ${CONCURRENCY:-default}"
  echo "benchmark:     ${BENCHMARK}"
  echo "sweep:         level_multiplier (l_base_max_bytes stays at default 64MB)"
  if [[ ${#EXTRA_OVERRIDES[@]} -gt 0 ]]; then
    echo "extra_overrides: ${EXTRA_OVERRIDES[*]}"
  else
    echo "extra_overrides: none"
  fi
  echo "cases:"
  for c in "${CASES[@]}"; do echo "  - $c"; done
} > "${RUN_DIR}/METADATA.txt"

cat "${RUN_DIR}/METADATA.txt"
echo "Results will be written to: ${RUN_DIR}"
echo

run_or_echo() {
  if [[ $DRY_RUN -eq 1 ]]; then
    printf '+ '; printf '%q ' "$@"; printf '\n'
  else
    "$@"
  fi
}

run_case() {
  local name="$1" mult="$2"
  local data_dir="${DATA_ROOT}/${name}"
  local json_out="${RUN_DIR}/${name}.json"
  local log_out="${RUN_DIR}/${name}.log"

  echo "─── case: ${name} (level_multiplier=${mult}) ──────────────────────"
  echo "data-dir: ${data_dir}"
  echo "json:     ${json_out}"
  echo "log:      ${log_out}"

  # Fresh state per case — write-amp is meaningless across runs that share a
  # data dir (the carry-over compaction debt would dominate).
  run_or_echo rm -rf "${data_dir}"
  run_or_echo mkdir -p "${data_dir}"

  local args=(
    "$PEBBLE_BENCH" run
    --benchmark "$BENCHMARK"
    --data-dir "$data_dir"
    --output json --output-file "$json_out"
    --log-file "$log_out"
    --override "benchmark.duration=${DURATION}"
    --override "level_multiplier=${mult}"
  )
  [[ -n "$CONCURRENCY" ]] && args+=( --concurrency "$CONCURRENCY" )
  for ov in ${EXTRA_OVERRIDES[@]+"${EXTRA_OVERRIDES[@]}"}; do
    args+=( --override "$ov" )
  done

  local start_ts; start_ts=$(date +%s)
  run_or_echo "${args[@]}"
  local end_ts; end_ts=$(date +%s)
  echo "  finished in $((end_ts - start_ts))s"

  run_or_echo rm -rf "${data_dir}"
  echo
}

TOTAL_START=$(date +%s)
for entry in "${CASES[@]}"; do
  name="${entry%%:*}"
  value="${entry#*:}"
  run_case "$name" "$value"
done
TOTAL_END=$(date +%s)

echo "All cases finished in $((TOTAL_END - TOTAL_START))s."
echo

if [[ $DRY_RUN -eq 0 ]]; then
  baseline_entry="${CASES[$BASELINE_INDEX]}"
  baseline_name="${baseline_entry%%:*}"
  baseline_json="${RUN_DIR}/${baseline_name}.json"

  echo "Comparisons against baseline: ${baseline_name} (Pebble default)"
  for entry in "${CASES[@]}"; do
    name="${entry%%:*}"
    [[ "$name" == "$baseline_name" ]] && continue
    out_txt="${RUN_DIR}/compare-${baseline_name}-vs-${name}.txt"
    out_md="${RUN_DIR}/compare-${baseline_name}-vs-${name}.md"
    "$PEBBLE_BENCH" compare "$baseline_json" "${RUN_DIR}/${name}.json" \
        > "$out_txt" 2>&1 || true
    "$PEBBLE_BENCH" compare --output-file "$out_md" \
        "$baseline_json" "${RUN_DIR}/${name}.json" >/dev/null 2>&1 || true
    echo "  ${baseline_name} → ${name}: ${out_txt}"
  done
  echo

  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -x "${SCRIPT_DIR}/summarize-sweep.sh" ]]; then
    "${SCRIPT_DIR}/summarize-sweep.sh" "$RUN_DIR" > "${RUN_DIR}/SUMMARY.md" || true
    echo "Summary written to: ${RUN_DIR}/SUMMARY.md"
    echo
    cat "${RUN_DIR}/SUMMARY.md"
  fi
fi

echo "Done. Inspect ${RUN_DIR}/ for full results."
