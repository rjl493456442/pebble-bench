package datagen

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
)

// Meta stores metadata about the populated dataset.
type Meta struct {
	TotalKeys uint64 `json:"total_keys"`
	KeySize   int    `json:"key_size"`
	ValueSize int    `json:"value_size"`

	// Populate records the write-side statistics of the run that built (or last
	// extended) the dataset, most importantly the write amplification. Pebble's
	// compaction counters live only in the process that did the writing, so this
	// is the only place they survive after the data directory is discarded. It is
	// nil for datasets written before this field existed.
	Populate *PopulateStats `json:"populate,omitempty"`
}

// PopulateStats captures how a population run behaved, so a comparison can be
// made from the retained metadata alone after the data directory is deleted.
type PopulateStats struct {
	// Profile is the tuning profile in force, "baseline" or "write-heavy".
	Profile string `json:"profile"`

	// NewKeys is how many keys this run wrote (excludes pre-existing keys when
	// extending), and Duration/OverallRate describe how long that took.
	NewKeys     uint64  `json:"new_keys"`
	DurationSec float64 `json:"duration_sec"`
	OverallRate float64 `json:"overall_keys_per_sec"`

	// WriteAmp is what Pebble itself reports. Its denominator is WAL.BytesWritten
	// (physical WAL bytes, inflated by WAL file recycling), so it is NOT
	// comparable across profiles that recycle the WAL differently. Prefer
	// WriteAmpLogical below for comparisons.
	WriteAmp float64 `json:"write_amp"`

	// WriteAmpLogical is the honest, cross-comparable write amplification: the raw
	// physical table bytes written by flushes and compactions divided by the
	// logical bytes the user actually ingested (WAL.BytesIn). Both the numerator
	// and denominator are recycling-independent.
	WriteAmpLogical float64 `json:"write_amp_logical"`

	BytesIn         uint64 `json:"bytes_in"`          // Pebble's denominator: WAL.BytesWritten + ingested
	BytesWritten    uint64 `json:"bytes_written"`     // flushed+compacted, incl. the WAL augmentation Pebble adds
	TableBytesRaw   uint64 `json:"table_bytes_raw"`   // BytesWritten minus the WAL augmentation: real SST bytes
	BytesRead       uint64 `json:"bytes_read"`        // bytes read during compaction
	WALBytesIn      uint64 `json:"wal_bytes_in"`      // logical bytes appended to the WAL (real ingest)
	WALBytesWritten uint64 `json:"wal_bytes_written"` // physical WAL bytes (recycling-inflated)

	// LogicalBytes is keys*(key+value); OnDiskBytes is Pebble's live+garbage disk
	// footprint at the end (DiskSpaceUsage), the ratio of the two being the space
	// amplification.
	LogicalBytes int64 `json:"logical_bytes"`
	OnDiskBytes  int64 `json:"on_disk_bytes"`

	// LevelSizes and LevelFiles are the final LSM shape (L0..L6).
	LevelSizes [7]int64 `json:"level_sizes"`
	LevelFiles [7]int64 `json:"level_files"`

	// WriteStall summarizes how long writers were blocked waiting for the LSM to
	// catch up (memtable or L0 back-pressure). StallPct — the fraction of the run
	// spent stalled — is the number that matters: it is throughput the profile
	// left on the table.
	StallCount    int64   `json:"stall_count"`
	StallTotalSec float64 `json:"stall_total_sec"`
	StallMaxSec   float64 `json:"stall_max_sec"`
	StallPct      float64 `json:"stall_pct_of_run"`
}

// SaveMeta writes metadata to a JSON file in the data directory.
func SaveMeta(dir string, meta *Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "bench_meta.json"), data, 0644)
}

// LoadMeta reads metadata from the data directory.
func LoadMeta(dir string) (*Meta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "bench_meta.json"))
	if err != nil {
		return nil, fmt.Errorf("loading metadata (have you run 'init' first?): %w", err)
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// KeyForIndex generates a deterministic key for the given index.
// The key is sha256(bigEndian(index)), producing uniformly distributed
// 32-byte keys similar to Ethereum state trie keys.
func KeyForIndex(index uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], index)
	h := sha256.Sum256(buf[:])
	return h[:]
}

// RandomValue generates a random value of the specified size.
func RandomValue(rng *rand.Rand, size int) []byte {
	val := make([]byte, size)
	for i := 0; i < size; i += 8 {
		v := rng.Int63()
		remaining := size - i
		if remaining >= 8 {
			binary.LittleEndian.PutUint64(val[i:], uint64(v))
		} else {
			for j := 0; j < remaining; j++ {
				val[i+j] = byte(v >> (j * 8))
			}
		}
	}
	return val
}
