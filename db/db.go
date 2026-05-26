package db

import (
	"errors"
	"io"
	"os"
	"strconv"
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

// Open opens a database using the backend selected by cfg.PebbleV2 and returns
// the database, whether commits should be synchronous, and a cleanup function
// that must be called on close.
func Open(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) (DB, bool, func(), error) {
	sync := !cfg.GetNoSync()
	if cfg.PebbleV2 {
		database, cleanup, err := openV2(cfg, flushTracker, writeStallTracker)
		return database, sync, cleanup, err
	}
	database, cleanup, err := openV1(cfg, flushTracker, writeStallTracker)
	return database, sync, cleanup, err
}

// OpenForInit opens a database optimized for bulk data loading.
func OpenForInit(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) (DB, bool, func(), error) {
	return Open(cfg, flushTracker, writeStallTracker)
}
