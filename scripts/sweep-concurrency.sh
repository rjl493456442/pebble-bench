#!/usr/bin/env bash
# sweep-concurrency.sh — sweep MaxConcurrentCompactions for the write benchmark.
#
# Same skeleton as sweep-lbase.sh / sweep-multiplier.sh. The varying knob is
# `max_concurrent_compactions` (Pebble v1: opts.MaxConcurrentCompactions;
# v2: the upper bound of CompactionConcurrencyRange). LBaseMaxBytes and
# LevelMultiplier are left at Pebble's defaults so concurrency is the only
# moving variable.
#
# Why this knob, in our context:
#   - The multiplier sweep showed the system gets +3-9% more disk throughput
#     just by making compactions smaller / less bursty, implying the device is
#     NOT saturated end-to-end — there's idle bandwidth between bursts.
#   - More concurrent compactions can fill that idle time, potentially raising
#     real (non-debt) write throughput without trading off ReadAmp.
#   - Unlike multiplier / target_file_size, this knob doesn't change LSM shape
#     — so any improvement here is "free" (no structural ReadAmp tax).
#
# Caveat:
#   - This knob caps the TOTAL number of concurrent compactions. L0→Lbase
#     concurrency is separately capped by opts.Experimental.L0CompactionConcurrency
#     (we currently hard-code it to 1). If L0→Lbase is the bottleneck, this
#     sweep may show modest gains and a follow-up `l0_compaction_concurrency`
#     sweep would be the next step.
#
# Default is runtime.NumCPU() at the buildV1Options layer (Pebble's own default
# is also based on the host CPU count). On a 32-core box, default = 32.
set -euo pipefail

#───────────────────────────────────────────────────────────────────────────────
# Defaults — fill bench-dir-derived ones AFTER argument parsing.
#───────────────────────────────────────────────────────────────────────────────
BENCH_DIR="${BENCH_DIR:-/home/gary/bench-env/pebble-bench/pebblev1}"
PEBBLE_BENCH="${PEBBLE_BENCH:-}"
DURATION="${DURATION:-20m}"
CONCURRENCY="${CONCURRENCY:-}"            # NOTE: this is the benchmark's worker
                                          # --concurrency, NOT pebble's
                                          # compaction concurrency.
BENCHMARK="${BENCHMARK:-write}"
DATA_ROOT="${DATA_ROOT:-}"
RESULTS_ROOT="${RESULTS_ROOT:-}"
EXTRA_OVERRIDES=()
DRY_RUN=0

# Sweep points. 32 sits where Pebble's default would land on a 32-core box;
# we go below (probe whether default is already over-provisioned), above
# (probe whether more parallelism helps), and well above (over-subscribe).
#
# If your host has != 32 cores, edit BASELINE_INDEX / the "32" entry so the
# baseline matches what pebble would have picked by default.
CASES=(
  "conc-4:4"
  "conc-8:8"
  "conc-16:16"
  "conc-32:32"
  "conc-64:64"
)
BASELINE_INDEX=3   # conc-32 (= runtime.NumCPU on the 32-core box)

usage() {
  cat <<EOF
Usage: $0 [options]

Sweeps Pebble's max_concurrent_compactions (the cap on total parallel
compactions). LBaseMaxBytes and LevelMultiplier stay at their defaults.

Options:
  --bench-dir <path>      Working directory containing pebble-bench (default: ${BENCH_DIR})
  --binary <path>         Path to the pebble-bench binary (default: \$BENCH_DIR/pebble-bench)
  --duration <dur>        Per-case duration, e.g. 10m, 20m (default: ${DURATION})
  --concurrency <n>       --concurrency forwarded to pebble-bench (worker count, NOT compaction!)
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

RUN_ID="$(date +%Y%m%d-%H%M%S)-conc"
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
  echo "concurrency:   ${CONCURRENCY:-default} (benchmark workers)"
  echo "benchmark:     ${BENCHMARK}"
  echo "sweep:         max_concurrent_compactions (l_base_max_bytes & level_multiplier at default)"
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
  local name="$1" conc="$2"
  local data_dir="${DATA_ROOT}/${name}"
  local json_out="${RUN_DIR}/${name}.json"
  local log_out="${RUN_DIR}/${name}.log"

  echo "─── case: ${name} (max_concurrent_compactions=${conc}) ──────────────────────"
  echo "data-dir: ${data_dir}"
  echo "json:     ${json_out}"
  echo "log:      ${log_out}"

  # Fresh state per case — compaction debt from a previous run would skew
  # write-amp on the next one.
  run_or_echo rm -rf "${data_dir}"
  run_or_echo mkdir -p "${data_dir}"

  local args=(
    "$PEBBLE_BENCH" run
    --benchmark "$BENCHMARK"
    --data-dir "$data_dir"
    --output json --output-file "$json_out"
    --log-file "$log_out"
    --override "benchmark.duration=${DURATION}"
    --override "max_concurrent_compactions=${conc}"
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

  echo "Comparisons against baseline: ${baseline_name} (= NumCPU on a 32-core box)"
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
