package db

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// Environment variables:
//   PEBBLE_BENCH_SLOW_FLUSH_THRESHOLD - duration threshold for slow flush warnings (e.g. "5s", "10s"). Default: disabled.
//   PEBBLE_BENCH_LOG_COMPACTION       - set to "1" or "true" to enable compaction begin/end logging. Default: disabled.
//   PEBBLE_BENCH_LOG_WRITE_STALL      - set to "1" or "true" to enable write stall begin/end logging. Default: disabled.

var (
	slowFlushThreshold time.Duration
	logCompaction      bool
	logWriteStall      bool
)

func init() {
	if v := os.Getenv("PEBBLE_BENCH_SLOW_FLUSH_THRESHOLD"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			slowFlushThreshold = d
		}
	}
	if v := os.Getenv("PEBBLE_BENCH_LOG_COMPACTION"); v != "" {
		logCompaction, _ = strconv.ParseBool(v)
	}
	if v := os.Getenv("PEBBLE_BENCH_LOG_WRITE_STALL"); v != "" {
		logWriteStall, _ = strconv.ParseBool(v)
	}
}

// formatLBase prints an LBaseMaxBytes override compactly, falling back to
// "default(64MB)" when no override was supplied.
func formatLBase(p *int64) string {
	if p == nil {
		return "default(64MB)"
	}
	v := *p
	switch {
	case v >= 1<<30:
		return fmt.Sprintf("%dGB", v>>30)
	case v >= 1<<20:
		return fmt.Sprintf("%dMB", v>>20)
	case v >= 1<<10:
		return fmt.Sprintf("%dKB", v>>10)
	default:
		return fmt.Sprintf("%dB", v)
	}
}

// derefIntOr returns *p when p != nil, otherwise the given default.
func derefIntOr(p *int, dflt int) int {
	if p == nil {
		return dflt
	}
	return *p
}

// defaultLevelTargetSizes are the go-ethereum default per-level target file
// sizes shared by both backends: 7 levels with target sizes doubling from 2MB
// to 128MB.
var defaultLevelTargetSizes = [7]int64{
	2 << 20,   // 2MB
	4 << 20,   // 4MB
	8 << 20,   // 8MB
	16 << 20,  // 16MB
	32 << 20,  // 32MB
	64 << 20,  // 64MB
	128 << 20, // 128MB
}

// ErrNotFound is returned by DB.Get when the requested key does not exist. Each
// backend translates its own not-found sentinel into this value so callers can
// compare against a single, version-independent error.
var ErrNotFound = errors.New("pebble: not found")

// DB is the version-agnostic key-value database interface used by the
// benchmarks. It is implemented by adapters around both Pebble v1 and v2.
type DB interface {
	// NewBatch creates a new write batch.
	NewBatch() Batch

	// Get returns the value for the given key. It returns ErrNotFound if the
	// key is absent. On success the caller must Close the returned closer.
	Get(key []byte) (value []byte, closer io.Closer, err error)

	// NewIter creates an iterator over the whole keyspace.
	NewIter() (Iterator, error)

	// Flush flushes the memtable(s) to stable storage.
	Flush() error

	// Metrics returns a normalized snapshot of the internal metrics.
	Metrics() *metrics.DBMetrics

	// ResolvedConfig returns the effective configuration actually used, with all
	// Pebble defaults materialized (for unambiguous result records).
	ResolvedConfig() *metrics.ResolvedConfig

	// Close closes the database and releases associated resources.
	Close() error
}

// Batch is a version-agnostic write batch.
type Batch interface {
	// Set adds a key/value pair to the batch.
	Set(key, value []byte) error

	// Commit applies the batch. When sync is true the commit waits for the WAL
	// to be persisted.
	Commit(sync bool) error

	// Close releases the batch resources.
	Close() error
}

// Iterator is a version-agnostic forward iterator.
type Iterator interface {
	First() bool
	Next() bool
	Valid() bool
	Error() error
	Close() error
}

// normalizeCompression maps a Pebble compression name (which differs slightly
// between versions, e.g. "Snappy" vs "NoCompression") to the lowercase token
// used in our config ("none"/"snappy"/"zstd"/"default"), passing through
// anything unrecognized.
func normalizeCompression(name string) string {
	switch strings.ToLower(name) {
	case "nocompression", "none":
		return "none"
	case "snappy":
		return "snappy"
	case "zstd", "zstdcompression":
		return "zstd"
	case "default", "defaultcompression":
		return "default"
	default:
		return strings.ToLower(name)
	}
}

// Open opens a database using the backend selected by cfg.PebbleV2 and returns
// the database, whether commits should be synchronous, and a cleanup function
// that must be called on close. A non-nil syncTracker instruments the VFS layer
// to count and time fsync/fdatasync/sync_file_range calls; a non-nil readTracker
// does the same for read/pread calls.
func Open(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker, syncTracker *metrics.SyncTracker, readTracker *metrics.ReadTracker, compactionTracker *metrics.CompactionTracker) (DB, bool, func(), error) {
	sync := !cfg.GetNoSync()
	if cfg.PebbleV2 {
		database, cleanup, err := openV2(cfg, flushTracker, writeStallTracker, syncTracker, readTracker, compactionTracker)
		return database, sync, cleanup, err
	}
	database, cleanup, err := openV1(cfg, flushTracker, writeStallTracker, syncTracker, readTracker, compactionTracker)
	return database, sync, cleanup, err
}

// OpenForInit opens a database optimized for bulk data loading.
func OpenForInit(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker, syncTracker *metrics.SyncTracker, readTracker *metrics.ReadTracker, compactionTracker *metrics.CompactionTracker) (DB, bool, func(), error) {
	return Open(cfg, flushTracker, writeStallTracker, syncTracker, readTracker, compactionTracker)
}
