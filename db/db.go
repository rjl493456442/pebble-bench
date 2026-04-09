package db

import (
	"fmt"
	"log"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// Open opens a Pebble database with the given config.
// Returns the DB, write options, and a cleanup function that must be called on close.
func Open(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) (*pebble.DB, *pebble.WriteOptions, func(), error) {
	opts, cacheCleanup := config.BuildPebbleOptions(cfg)

	// Set up event listener for logging compaction events, tracking flushes and write stalls
	opts.EventListener = &pebble.EventListener{
		CompactionBegin: func(info pebble.CompactionInfo) {
			log.Printf("compaction L%d -> L%d started", info.Input[0].Level, info.Output.Level)
		},
		CompactionEnd: func(info pebble.CompactionInfo) {
			log.Printf("compaction L%d -> L%d completed", info.Input[0].Level, info.Output.Level)
		},
		FlushEnd: func(info pebble.FlushInfo) {
			flushTracker.Record(info.Duration, info.InputBytes)
			if info.Duration > time.Second*10 {
				tables := len(info.Output)
				log.Printf("Slow flush detected, duration: %v, bytes: %s, output-tables: %d", info.Duration, metrics.FormatSize(info.InputBytes), tables)
			}
		},
		WriteStallBegin: func(info pebble.WriteStallBeginInfo) {
			writeStallTracker.Begin()
			log.Printf("Write stall begin reason: %s", info.Reason)
		},
		WriteStallEnd: func() {
			writeStallTracker.End()
			log.Printf("Write stall end")
		},
	}

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
