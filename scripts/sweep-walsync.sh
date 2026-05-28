#!/usr/bin/env bash
# sweep-walsync.sh — sweep wal_bytes_per_sync for the write benchmark.
#
# Same skeleton as the other sweep scripts. The varying knob is
# `wal_bytes_per_sync` (Pebble's opts.WALBytesPerSync; default 512KB).
#
# What this knob actually does:
#   - Controls how often the WAL writer issues a *background*
#     sync_file_range(WAIT_BEFORE | WRITE) while appending bytes.
#   - It is NOT WAL durability: that's controlled by `no_sync` (commit sync)
#     and by file-close fsync at memtable rotation.
#   - The purpose is to spread the dirty-page flush load across the WAL's
#     lifetime instead of bursting it all at file-close time, smoothing tail
#     latency.
#
# WAL lifetime size ≈ memtable size:
#   Each memtable maps 1:1 to a WAL file (a new WAL is created when memtable
#   rotates). So a 256MB memtable means each WAL file accumulates up to ~256MB
#   before being closed.
#
# Sweep choice (with default 256MB memtables):
#   - 256KB:  ~1024 SFR per WAL file (twice as frequent as default; tests the
#             cost of MORE syscalls).
#   - 512KB:  ~512 SFR  (Pebble default).
#   - 4MB:    ~64 SFR   (coarser).
#   - 32MB:   ~8 SFR    (much coarser).
#   - 256MB:  ~1 SFR    (effectively disabled — entire WAL in one shot at
#             close time).
#
# What to expect:
#   - On a fast SSD the tail-latency benefit of frequent SFR is small; on a
#     slow / queue-deep device it matters more.
#   - Larger values trade SFR syscall count for bursty page flushes at WAL
#     rotation. If those bursts coincide with foreground writes, p99.9 may
#     jump.
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

# Case format: "<case-name>:<value-in-bytes>". The case name is human-readable
# so the SUMMARY row tells you the value at a glance; the second field is raw
# bytes because that's what the override key wants (no suffix parsing).
CASES=(
  "wal-256kb:262144"
  "wal-512kb:524288"
  "wal-4mb:4194304"
  "wal-32mb:33554432"
  "wal-256mb:268435456"
)
BASELINE_INDEX=1   # wal-512kb = Pebble default

usage() {
  cat <<EOF
Usage: $0 [options]

Sweeps Pebble's wal_bytes_per_sync (default 512KB) — the threshold at which
the WAL writer issues a background sync_file_range during appends. All other
knobs (LBaseMaxBytes, LevelMultiplier, mem_table_size, etc.) stay at default.

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

RUN_ID="$(date +%Y%m%d-%H%M%S)-walsync"
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
  echo "sweep:         wal_bytes_per_sync (others at default)"
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
  local name="$1" bytes="$2"
  local data_dir="${DATA_ROOT}/${name}"
  local json_out="${RUN_DIR}/${name}.json"
  local log_out="${RUN_DIR}/${name}.log"

  echo "─── case: ${name} (wal_bytes_per_sync=${bytes} bytes) ──────────────────────"
  echo "data-dir: ${data_dir}"
  echo "json:     ${json_out}"
  echo "log:      ${log_out}"

  # Fresh state per case — write-amp must not inherit compaction debt from a
  # previous run.
  run_or_echo rm -rf "${data_dir}"
  run_or_echo mkdir -p "${data_dir}"

  local args=(
    "$PEBBLE_BENCH" run
    --benchmark "$BENCHMARK"
    --data-dir "$data_dir"
    --output json --output-file "$json_out"
    --log-file "$log_out"
    --override "benchmark.duration=${DURATION}"
    --override "wal_bytes_per_sync=${bytes}"
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

  echo "Comparisons against baseline: ${baseline_name} (Pebble default 512KB)"
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
