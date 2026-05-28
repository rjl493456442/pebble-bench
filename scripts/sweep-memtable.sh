#!/usr/bin/env bash
# sweep-memtable.sh — sweep memtable size for the write benchmark.
#
# Same skeleton as the other sweep scripts. The varying knob is `mem_table_size`
# (passed in MB, converted to bytes for the override). LBaseMaxBytes,
# LevelMultiplier and max_concurrent_compactions all stay at their defaults.
#
# Why this knob, in our context (informed by the prior sweeps):
#   - Each memtable flush becomes one L0 sublevel. With ~256MB memtables and a
#     500MB cap on L0→Lbase compactions, the picker already hits the cap at
#     two sublevels — so raising L0CompactionThreshold doesn't change per-
#     compaction L0 input.
#   - The remaining lever for "L0→Lbase passenger ratio" is the SUBLEVEL SIZE
#     itself: smaller memtables → smaller per-sublevel bytes → more sublevels
#     can pack under the 500MB cap per compaction → in random-hash workload,
#     each L0→Lbase rewrites less Lbase per byte of L0 data.
#   - The countervailing cost: smaller memtables flush more often → more fsync
#     traffic on flush, more sublevels accumulate before each compaction
#     (read amp transient).
#
# Two regions to probe:
#   - BELOW the default (32/64/128MB): our hypothesis — smaller flushes pack
#     more sublevels per L0→Lbase, reducing real write amp.
#   - ABOVE the default (~511MB, just under the v1MaxMemTableSize cap of
#     512MB-1): does pushing memtables to their max give back anything, or
#     hit the same cap-vs-input bind we saw with the multiplier sweep?
#
# Note: this script edits per-memtable SIZE only. The mem_table_count knob
# (default 4) and mem_table_stop_writes_threshold (default 2×count) are
# untouched, so the total memtable RAM budget scales linearly with the size
# you pick (e.g. 32MB×4 = 128MB total ↔ 256MB×4 = 1GB total).
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

# Sweep points. Values are in MB; the script converts to bytes for the
# override (mem_table_size still expects raw bytes, not a suffix).
# 256MB matches the default for the standard cache_mb=2048, mem_table_count=4
# configuration (cache/2/count = 2048/2/4 = 256). 511MB is the v1 hard cap
# (v1MaxMemTableSize - 1).
CASES=(
  "mt-32mb:32"
  "mt-64mb:64"
  "mt-128mb:128"
  "mt-256mb:256"
  "mt-511mb:511"
)
BASELINE_INDEX=3   # mt-256mb = current default for cache_mb=2048

usage() {
  cat <<EOF
Usage: $0 [options]

Sweeps Pebble's mem_table_size (in MB). Smaller memtables produce smaller L0
sublevels, which can pack more sublevels under the 500MB L0→Lbase cap per
compaction — the only remaining lever for reducing the random-hash L0→Lbase
passenger ratio on this workload.

Options:
  --bench-dir <path>      Working directory containing pebble-bench (default: ${BENCH_DIR})
  --binary <path>         Path to the pebble-bench binary (default: \$BENCH_DIR/pebble-bench)
  --duration <dur>        Per-case duration, e.g. 10m, 20m (default: ${DURATION})
  --concurrency <n>       --concurrency forwarded to pebble-bench (worker count)
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

RUN_ID="$(date +%Y%m%d-%H%M%S)-mt"
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
  echo "sweep:         mem_table_size (others at default)"
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
  local name="$1" mt_mb="$2"
  local mt_bytes=$(( mt_mb * 1024 * 1024 ))
  local data_dir="${DATA_ROOT}/${name}"
  local json_out="${RUN_DIR}/${name}.json"
  local log_out="${RUN_DIR}/${name}.log"

  echo "─── case: ${name} (mem_table_size=${mt_mb}MB / ${mt_bytes} bytes) ──────────────────────"
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
    --override "mem_table_size=${mt_bytes}"
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

  echo "Comparisons against baseline: ${baseline_name} (= default 256MB for cache_mb=2048)"
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
