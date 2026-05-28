#!/usr/bin/env bash
# sweep-lbase.sh — sweep LBaseMaxBytes for the write benchmark.
#
# Runs the pebble-bench `write` benchmark once per LBaseMaxBytes value, with a
# fresh data directory each time so write amplification is measured from a
# clean slate. Everything is set via --override (no YAML), so the script is
# fully self-contained.
#
# Outputs per case:
#   results/<run-id>/<case-name>.json   — full JSON report
#   results/<run-id>/<case-name>.log    — stdout/stderr from the run
# Plus at the end:
#   results/<run-id>/compare-<baseline>-vs-<case>.{txt,md}
#   results/<run-id>/SUMMARY.md         — one-table view across all cases
#
# Usage:
#   ./sweep-lbase.sh                     # run all cases with defaults
#   ./sweep-lbase.sh --duration 10m      # shorter cases
#   ./sweep-lbase.sh --dry-run           # print commands without running

set -euo pipefail

#───────────────────────────────────────────────────────────────────────────────
# Defaults — override via flags. Anything bench-dir-derived is filled in AFTER
# argument parsing so --bench-dir actually re-roots the layout.
#───────────────────────────────────────────────────────────────────────────────
BENCH_DIR="${BENCH_DIR:-/home/gary/bench-env/pebble-bench/pebblev1}"
PEBBLE_BENCH="${PEBBLE_BENCH:-}"
DURATION="${DURATION:-20m}"
CONCURRENCY="${CONCURRENCY:-}"          # empty → use pebble-bench's default
BENCHMARK="${BENCHMARK:-write}"
DATA_ROOT="${DATA_ROOT:-}"
RESULTS_ROOT="${RESULTS_ROOT:-}"
EXTRA_OVERRIDES=()                       # any additional --override key=value
DRY_RUN=0

# The actual sweep. Format: "case-name:l_base_max_bytes_value".
# 64MB is the Pebble default and serves as the baseline.
CASES=(
  "lbase-64mb:64MB"
  "lbase-128mb:128MB"
  "lbase-256mb:256MB"
  "lbase-512mb:512MB"
  "lbase-1gb:1GB"
)
BASELINE_INDEX=0   # which CASES entry is the baseline that others are compared against

#───────────────────────────────────────────────────────────────────────────────
# Argument parsing
#───────────────────────────────────────────────────────────────────────────────
usage() {
  cat <<EOF
Usage: $0 [options]

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

# Fill in any bench-dir-derived defaults now that --bench-dir has been seen.
: "${PEBBLE_BENCH:=${BENCH_DIR}/pebble-bench}"
: "${DATA_ROOT:=${BENCH_DIR}/sweep-data}"
: "${RESULTS_ROOT:=${BENCH_DIR}/sweep-results}"

#───────────────────────────────────────────────────────────────────────────────
# Preflight
#───────────────────────────────────────────────────────────────────────────────
if [[ ! -x "$PEBBLE_BENCH" ]]; then
  echo "pebble-bench binary not found or not executable: $PEBBLE_BENCH" >&2
  exit 1
fi

RUN_ID="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="${RESULTS_ROOT}/${RUN_ID}"
mkdir -p "$RUN_DIR"

# A meta file describing how this sweep was invoked. Useful when you come back
# to old results months later and need to remember what was varied.
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

#───────────────────────────────────────────────────────────────────────────────
# Helpers
#───────────────────────────────────────────────────────────────────────────────
run_or_echo() {
  if [[ $DRY_RUN -eq 1 ]]; then
    printf '+ '; printf '%q ' "$@"; printf '\n'
  else
    "$@"
  fi
}

run_case() {
  local name="$1" lbase="$2"
  local data_dir="${DATA_ROOT}/${name}"
  local json_out="${RUN_DIR}/${name}.json"
  local log_out="${RUN_DIR}/${name}.log"

  echo "─── case: ${name} (l_base_max_bytes=${lbase}) ──────────────────────"
  echo "data-dir: ${data_dir}"
  echo "json:     ${json_out}"
  echo "log:      ${log_out}"

  # Fresh state for every case — otherwise compaction debt from a previous run
  # would skew write-amp on the next one.
  run_or_echo rm -rf "${data_dir}"
  run_or_echo mkdir -p "${data_dir}"

  local args=(
    "$PEBBLE_BENCH" run
    --benchmark "$BENCHMARK"
    --data-dir "$data_dir"
    --output json --output-file "$json_out"
    --log-file "$log_out"
    --override "benchmark.duration=${DURATION}"
    --override "l_base_max_bytes=${lbase}"
  )
  [[ -n "$CONCURRENCY" ]] && args+=( --concurrency "$CONCURRENCY" )
  # ${arr[@]+...} expands only when the array has at least one element; this
  # is the canonical guard against `set -u` complaining about empty arrays on
  # older bash (3.2 on macOS, etc.).
  for ov in ${EXTRA_OVERRIDES[@]+"${EXTRA_OVERRIDES[@]}"}; do
    args+=( --override "$ov" )
  done

  local start_ts; start_ts=$(date +%s)
  run_or_echo "${args[@]}"
  local end_ts; end_ts=$(date +%s)
  echo "  finished in $((end_ts - start_ts))s"

  # Per-case clean-up: the data dir of this case is no longer needed once we
  # have its JSON report. Drop it so the next case has the full disk to work
  # with (compaction is sensitive to free space).
  run_or_echo rm -rf "${data_dir}"
  echo
}

#───────────────────────────────────────────────────────────────────────────────
# Run all cases
#───────────────────────────────────────────────────────────────────────────────
TOTAL_START=$(date +%s)
for entry in "${CASES[@]}"; do
  name="${entry%%:*}"
  value="${entry#*:}"
  run_case "$name" "$value"
done
TOTAL_END=$(date +%s)

echo "All cases finished in $((TOTAL_END - TOTAL_START))s."
echo

#───────────────────────────────────────────────────────────────────────────────
# Comparisons: baseline vs each other case (skipped on --dry-run because there
# are no JSON files to compare against).
#───────────────────────────────────────────────────────────────────────────────
if [[ $DRY_RUN -eq 0 ]]; then
  baseline_entry="${CASES[$BASELINE_INDEX]}"
  baseline_name="${baseline_entry%%:*}"
  baseline_json="${RUN_DIR}/${baseline_name}.json"

  echo "Comparisons against baseline: ${baseline_name}"
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

  # One-shot summary table across all cases for the eye-grep view.
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -x "${SCRIPT_DIR}/summarize-sweep.sh" ]]; then
    "${SCRIPT_DIR}/summarize-sweep.sh" "$RUN_DIR" > "${RUN_DIR}/SUMMARY.md" || true
    echo "Summary written to: ${RUN_DIR}/SUMMARY.md"
    echo
    cat "${RUN_DIR}/SUMMARY.md"
  fi
fi

echo "Done. Inspect ${RUN_DIR}/ for full results."
