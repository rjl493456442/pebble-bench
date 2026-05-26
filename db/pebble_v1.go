package db

import (
	"fmt"
	"io"
	"log"

	"github.com/cockroachdb/pebble"
	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// openV1 opens a Pebble v1 database and wraps it in the version-agnostic DB
// interface.
func openV1(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) (DB, func(), error) {
	log.Printf("Opening database with Pebble v1")
	opts, cacheCleanup := config.BuildPebbleOptions(cfg)
	opts.EventListener = newV1Listener(flushTracker, writeStallTracker)

	database, err := pebble.Open(cfg.DataDir, opts)
	if err != nil {
		cacheCleanup()
		return nil, nil, fmt.Errorf("opening pebble v1 database: %w", err)
	}

	cleanup := func() {
		if err := database.Close(); err != nil {
			log.Printf("error closing database: %v", err)
		}
		cacheCleanup()
	}
	return &v1DB{db: database}, cleanup, nil
}

// newV1Listener builds the event listener that records flush, write-stall and
// (optionally) compaction events for a Pebble v1 database.
func newV1Listener(flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) *pebble.EventListener {
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
	return listener
}

// v1DB adapts *pebble.DB (v1) to the DB interface.
type v1DB struct {
	db *pebble.DB
}

func (d *v1DB) NewBatch() Batch { return &v1Batch{batch: d.db.NewBatch()} }

func (d *v1DB) Get(key []byte) ([]byte, io.Closer, error) {
	value, closer, err := d.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil, ErrNotFound
	}
	return value, closer, err
}

func (d *v1DB) NewIter() (Iterator, error) {
	iter, err := d.db.NewIter(&pebble.IterOptions{})
	if err != nil {
		return nil, err
	}
	return iter, nil
}

func (d *v1DB) Flush() error { return d.db.Flush() }

func (d *v1DB) Close() error { return d.db.Close() }

func (d *v1DB) Metrics() *metrics.DBMetrics {
	m := d.db.Metrics()
	out := &metrics.DBMetrics{
		DiskSpaceUsage:    m.DiskSpaceUsage(),
		ReadAmplification: int(m.ReadAmp()),
		CompactionCount:   m.Compact.Count,
		CompactionDebt:    m.Compact.EstimatedDebt,
		CompactionsActive: m.Compact.NumInProgress,
		MemTableSize:      m.MemTable.Size,
		MemTableCount:     m.MemTable.Count,
		BlockCacheHits:    m.BlockCache.Hits,
		BlockCacheMisses:  m.BlockCache.Misses,
		TableCacheHits:    m.TableCache.Hits,
		TableCacheMisses:  m.TableCache.Misses,
		FilterHits:        m.Filter.Hits,
		FilterMisses:      m.Filter.Misses,
	}
	for i, l := range m.Levels {
		if i < len(out.LevelSizes) {
			out.LevelSizes[i] = l.Size
			out.LevelFiles[i] = l.NumFiles
		}
	}
	return out
}

// v1Batch adapts *pebble.Batch (v1) to the Batch interface.
type v1Batch struct {
	batch *pebble.Batch
}

func (b *v1Batch) Set(key, value []byte) error { return b.batch.Set(key, value, nil) }

func (b *v1Batch) Commit(sync bool) error {
	opts := pebble.NoSync
	if sync {
		opts = pebble.Sync
	}
	return b.batch.Commit(opts)
}

func (b *v1Batch) Close() error { return b.batch.Close() }
