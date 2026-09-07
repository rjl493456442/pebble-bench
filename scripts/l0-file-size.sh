#!/usr/bin/env bash
# l0-file-size.sh — the A/B behind go-ethereum's L0 file size change.
#
# Usage: scripts/l0-file-size.sh <out-dir> [data-parent]
#
# Runs the write benchmark under two workloads and three configurations, each
# from a fresh database, and writes the pairwise comparisons:
#
#   workloads   random    32B sha256 keys, 224B incompressible values
#               snapsync  16 append-only streams over disjoint hash ranges,
#                         10% random-key batches, values compressing to ~0.7
#   configs     baseline        geth as it was: 2MB L0 files, threshold 2
#               l0-16mb-thr2    only the L0 file size raised
#               l0-16mb         the change as proposed: 16MB and threshold 4
#
# Each run is 30 minutes of writing plus up to 10 minutes of settle, so the
# whole thing takes about four hours. Keep the machine otherwise idle; the
# claim under test is about the disk.
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <out-dir> [data-parent]" >&2
  exit 2
fi
OUT="$1"
DATA="${2:-./l0-bench-data}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$OUT" "$DATA"

( cd "$ROOT" && go build -o "$OUT/pebble-bench" . )
BIN="$OUT/pebble-bench"

{
  echo "date:    $(date -u +%FT%TZ)"
  echo "commit:  $(git -C "$ROOT" rev-parse HEAD) $(git -C "$ROOT" status --porcelain | grep -q . && echo '(dirty)')"
  echo "go:      $(go version)"
  echo "host:    $(uname -srm) $(nproc 2>/dev/null || sysctl -n hw.ncpu) cores"
  echo "disk:    $(df -h "$DATA" | tail -1)"
} | tee "$OUT/METADATA.txt"

run() {  # run <workload> <config> <overrides...>
  local wl="$1" cfg="$2"; shift 2
  local name="${wl}-${cfg}" dir="$DATA/$wl-$cfg"
  rm -rf "$dir"
  echo "=== $name  $(date +%T)"
  "$BIN" run --pebblev2 --config "$ROOT/config/profiles/geth-$cfg.yaml" --data-dir "$dir" \
    --override benchmark.seed=20260904 "$@" \
    --output json --output-file "$OUT/$name.json" --log-file "$OUT/$name.log" > /dev/null
  echo "=== $name done $(date +%T), on disk $(du -sh "$dir" | cut -f1)"
  rm -rf "$dir"
}

RANDOM_WL=(--override benchmark.key_pattern=random)
SNAPSYNC_WL=(--override benchmark.key_pattern=snapsync:16 --override benchmark.random_fraction=0.1 --override benchmark.value_entropy=0.7)

for cfg in baseline l0-16mb-thr2 l0-16mb; do
  run random "$cfg" "${RANDOM_WL[@]}"
done
for cfg in baseline l0-16mb-thr2 l0-16mb; do
  run snapsync "$cfg" "${SNAPSYNC_WL[@]}"
done

for wl in random snapsync; do
  "$BIN" compare "$OUT/$wl-baseline.json"     "$OUT/$wl-l0-16mb.json"      > "$OUT/compare-$wl-baseline-vs-16mb.txt"
  "$BIN" compare "$OUT/$wl-baseline.json"     "$OUT/$wl-l0-16mb-thr2.json" > "$OUT/compare-$wl-baseline-vs-16mb-thr2.txt"
  "$BIN" compare "$OUT/$wl-l0-16mb-thr2.json" "$OUT/$wl-l0-16mb.json"      > "$OUT/compare-$wl-thr2-vs-thr4.txt"
done
echo "=== all done $(date +%T); comparisons in $OUT/compare-*.txt"
