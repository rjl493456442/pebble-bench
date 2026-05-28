#!/usr/bin/env bash
# summarize-sweep.sh — build a one-table markdown summary across a sweep dir.
#
# Usage: summarize-sweep.sh <run-dir>
#
# Reads every *.json result in <run-dir>, pulls the headline metrics
# (ops/sec, p99, write-amp, read-amp, bytes_written, fsync count/avg, …) and
# emits a single markdown table sorted by ops/sec so the winner is at the top.
#
# Requires jq.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 <run-dir>" >&2
  exit 2
fi
RUN_DIR="$1"
[[ -d "$RUN_DIR" ]] || { echo "not a directory: $RUN_DIR" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

echo "# Pebble sweep summary"
echo
if [[ -f "${RUN_DIR}/METADATA.txt" ]]; then
  echo '```'
  cat "${RUN_DIR}/METADATA.txt"
  echo '```'
  echo
fi

# Column choices = the ones I actually look at when picking a winner:
# throughput, tail latency, the amp triangle, device-pressure proxies (fsync
# count/avg, sync_file_range avg) — plus both leveling knobs so the table is
# self-explanatory regardless of which one is being swept.
echo "| Case | LBase | Mult | Ops/sec | avg | p99 | p99.9 | max | Write Amp | Bytes W | Read Amp(avg) | Compactions | Stalls | fsync cnt | fsync avg | fdatasync avg | SFR avg |"
echo "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|"

# Single jq program that emits one TSV row per file. jq does the heavy lifting:
# computes averages (total_time/count) and formats bytes / durations into the
# same units the human reporter uses, so we don't need shell-side math.
JQ_PROG='
  def fmt_bytes:
    if . == null or . == 0 then "0"
    elif . >= 1073741824 then (./1073741824*100|round/100|tostring)+" GB"
    elif . >= 1048576    then (./1048576*10|round/10|tostring)+" MB"
    else (./1024|round|tostring)+" KB" end;

  def fmt_dur_ns:
    if . == null or . == 0 then "0"
    elif . >= 1e9 then ((./1e9*100|round/100)|tostring)+"s"
    elif . >= 1e6 then ((./1e6*100|round/100)|tostring)+"ms"
    elif . >= 1e3 then ((./1e3*100|round/100)|tostring)+"µs"
    else (.|tostring)+"ns" end;

  def fmt_dur_us:
    if . == null or . == 0 then "0"
    elif . >= 1e9 then ((./1e9*100|round/100)|tostring)+"s"
    elif . >= 1e6 then ((./1e6*100|round/100)|tostring)+"ms"
    elif . >= 1e3 then ((./1e3*100|round/100)|tostring)+"µs"
    else (.|tostring)+"ns" end;

  def fmt_lbase:
    if . == null or . == 0 then "default"
    elif . >= 1073741824 then ((./1073741824)|tostring)+"GB"
    elif . >= 1048576    then ((./1048576)|tostring)+"MB"
    else ((./1024)|tostring)+"KB" end;

  def avg_ns(stat): (stat.total_time // 0) as $t | (stat.count // 0) as $c |
    if $c > 0 then $t / $c else 0 end;

  # us-based latency from the Summary block (avg_us / p99_us / etc.) is stored
  # as integer microseconds. Convert to ns for the formatter.
  def us_to_ns: (. // 0) * 1000;

  [
    $name,
    ((.config.pebble.l_base_max_bytes // 0) | fmt_lbase),
    (.config.pebble.level_multiplier // 0),
    (.summary.ops_per_sec // 0),
    ((.summary.avg_us  // 0) | us_to_ns | fmt_dur_ns),
    ((.summary.p99_us  // 0) | us_to_ns | fmt_dur_ns),
    ((.summary.p999_us // 0) | us_to_ns | fmt_dur_ns),
    ((.summary.max_us  // 0) | us_to_ns | fmt_dur_ns),
    (.pebble_final.WriteAmp // 0),
    ((.pebble_final.BytesWritten // 0) | fmt_bytes),
    (.read_amp_avg // (.pebble_final.ReadAmplification // 0)),
    (.pebble_final.CompactionCount // 0),
    (.pebble_final.write_stall_stats.count // 0),
    (.pebble_final.sync_stats.fsync.count // 0),
    (avg_ns(.pebble_final.sync_stats.fsync)           | fmt_dur_ns),
    (avg_ns(.pebble_final.sync_stats.fdatasync)       | fmt_dur_ns),
    (avg_ns(.pebble_final.sync_stats.sync_file_range) | fmt_dur_ns)
  ] | @tsv
'

for f in "${RUN_DIR}"/*.json; do
  [[ -f "$f" ]] || continue
  base="$(basename "$f" .json)"
  case "$base" in compare-*) continue ;; esac
  jq -r --arg name "$base" "$JQ_PROG" "$f"
done | sort -t$'\t' -k4,4 -g -r | \
while IFS=$'\t' read -r name lbase mult ops avg p99 p999 mx wamp bw ramp comps stalls fsc fs_avg fdb_avg sfr_avg; do
  printf "| %s | %s | %s | %.0f | %s | %s | %s | %s | %.2f | %s | %.2f | %d | %d | %d | %s | %s | %s |\n" \
    "$name" "$lbase" "$mult" "$ops" "$avg" "$p99" "$p999" "$mx" \
    "$wamp" "$bw" "$ramp" "$comps" "$stalls" "$fsc" \
    "$fs_avg" "$fdb_avg" "$sfr_avg"
done

echo
echo "_Sorted by ops/sec descending. \"SFR\" = sync_file_range. \`Read Amp(avg)\` is the run-wide average (\`read_amp_avg\`), falling back to the final snapshot if missing._"
