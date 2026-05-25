package db

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/cockroachdb/pebble"
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

// Open opens a Pebble database with the given config.
// Returns the DB, write options, and a cleanup function that must be called on close.
func Open(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) (*pebble.DB, *pebble.WriteOptions, func(), error) {
	opts, cacheCleanup := config.BuildPebbleOptions(cfg)

	// Set up event listener for tracking flushes, write stalls, and optional logging
	listener := &pebble.EventListener{
		FlushEnd: func(info pebble.FlushInfo) {
			flushTracker.Record(info.Duration, info.InputBytes)
			if slowFlushThreshold > 0 && info.Duration > slowFlushThreshold {
				log.Printf("Slow flush detected, duration: %v, bytes: %s, output-tables: %d",
					info.Duration, metrics.FormatSize(info.InputBytes), len(info.Output))
			}
		},
		WriteStallBegin: func(info pebble.WriteStallBeginInfo) {
			writeStallTracker.Begin()
			if logWriteStall {
				log.Printf("Write stall begin reason: %s", info.Reason)
			}
		},
		WriteStallEnd: func() {
			writeStallTracker.End()
			if logWriteStall {
				log.Printf("Write stall end")
			}
		},
	}
	if logCompaction {
		listener.CompactionBegin = func(info pebble.CompactionInfo) {
			log.Printf("compaction L%d -> L%d started", info.Input[0].Level, info.Output.Level)
		}
		listener.CompactionEnd = func(info pebble.CompactionInfo) {
			log.Printf("compaction L%d -> L%d completed", info.Input[0].Level, info.Output.Level)
		}
	}
	opts.EventListener = listener

	database, err := pebble.Open(cfg.DataDir, opts)
	if err != nil {
		cacheCleanup()
		return nil, nil, nil, fmt.Errorf("opening pebble database: %w", err)
	}

	writeOpts := pebble.Sync
	if cfg.GetNoSync() {
		writeOpts = pebble.NoSync
	}

	cleanup := func() {
		if err := database.Close(); err != nil {
			log.Printf("error closing database: %v", err)
		}
		cacheCleanup()
	}
	return database, writeOpts, cleanup, nil
}

// OpenForInit opens a Pebble database optimized for bulk data loading.
func OpenForInit(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) (*pebble.DB, *pebble.WriteOptions, func(), error) {
	return Open(cfg, flushTracker, writeStallTracker)
}
