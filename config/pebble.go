package config

import (
	"log"
	"runtime"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/bloom"
)

const maxMemTableSize = 512 << 20 // 512MB

// BuildPebbleOptions translates a BenchConfig into pebble.Options.
// The returned cleanup function must be called to release the cache.
func BuildPebbleOptions(cfg *BenchConfig) (*pebble.Options, func()) {
	cacheSize := int64(cfg.CacheMB) * 1024 * 1024
	cache := pebble.NewCache(cacheSize)

	memTableCount := cfg.GetMemTableCount()
	var memTableSize int
	if cfg.MemTableSize != nil {
		memTableSize = *cfg.MemTableSize
	} else {
		memTableSize = int(cacheSize) / 2 / memTableCount
	}
	if memTableSize >= maxMemTableSize {
		memTableSize = maxMemTableSize - 1
	}

	// MemTableStopWritesThreshold places a hard limit on the number
	// of the existent MemTables(including the frozen one).
	//
	// Note, this must be the number of tables not the size of all memtables
	// according to https://github.com/cockroachdb/pebble/blob/master/options.go#L738-L742
	// and to https://github.com/cockroachdb/pebble/blob/master/db.go#L1892-L1903.
	//
	// MemTableStopWritesThreshold is set to twice the maximum number of
	// allowed memtables to accommodate temporary spikes.
	memTableStopWrites := memTableCount * 2
	if cfg.MemTableStopWritesThreshold != nil {
		memTableStopWrites = *cfg.MemTableStopWritesThreshold
	}

	maxConcurrentCompactions := runtime.NumCPU()
	if cfg.MaxConcurrentCompactions != nil {
		maxConcurrentCompactions = *cfg.MaxConcurrentCompactions
	}

	// L0CompactionThreshold specifies the number of L0 read-amplification
	// necessary to trigger an L0 compaction. It essentially refers to the
	// number of sub-levels at the L0. For each sub-level, it contains several
	// L0 files which are non-overlapping with each other, typically produced
	// by a single memory-table flush.
	//
	// The default value in Pebble is 4, which is a bit too large to have
	// the compaction debt as around 10GB. By reducing it to 2, the compaction
	// debt will be less than 1GB, but with more frequent compactions scheduled.
	l0CompactionThreshold := 2
	if cfg.L0CompactionThreshold != nil {
		l0CompactionThreshold = *cfg.L0CompactionThreshold
	}

	l0StopWritesThreshold := 12
	if cfg.L0StopWritesThreshold != nil {
		l0StopWritesThreshold = *cfg.L0StopWritesThreshold
	}

	// Pebble is configured to use asynchronous write mode, meaning write operations
	// return as soon as the data is cached in memory, without waiting for the WAL
	// to be written. This mode offers better write performance but risks losing
	// recent writes if the application crashes or a power failure/system crash occurs.
	//
	// By setting the WALBytesPerSync, the cached WAL writes will be periodically
	// flushed at the background if the accumulated size exceeds this threshold.
	walBytesPerSync := 512 * 1024
	if cfg.WALBytesPerSync != nil {
		walBytesPerSync = *cfg.WALBytesPerSync
	}

	bloomBits := 10
	if cfg.BloomFilterBits != nil {
		bloomBits = *cfg.BloomFilterBits
	}

	// Build level options
	levels := buildLevelOptions(cfg.Levels, bloomBits)

	opts := &pebble.Options{
		Cache:                       cache,
		MaxOpenFiles:                cfg.Handles,
		MemTableSize:                uint64(memTableSize),
		MemTableStopWritesThreshold: memTableStopWrites,
		MaxConcurrentCompactions: func() int {
			return maxConcurrentCompactions
		},
		L0CompactionThreshold: l0CompactionThreshold,
		L0StopWritesThreshold: l0StopWritesThreshold,
		Levels:                levels,
		ReadOnly:              cfg.ReadOnly,
		WALBytesPerSync:       walBytesPerSync,
	}

	if cfg.DisableWAL != nil && *cfg.DisableWAL {
		opts.DisableWAL = true
	}

	// Experimental options (go-ethereum defaults)
	if cfg.ReadSamplingMultiplier != nil {
		opts.Experimental.ReadSamplingMultiplier = *cfg.ReadSamplingMultiplier
	} else {
		opts.Experimental.ReadSamplingMultiplier = -1
	}
	// These two settings define the conditions under which compaction concurrency
	// is increased. Specifically, one additional compaction job will be enabled when:
	// - there is one more overlapping sub-level0;
	// - there is an additional 256 MB of compaction debt;
	//
	// The maximum concurrency is still capped by MaxConcurrentCompactions, but with
	// these settings compactions can scale up more readily.
	if cfg.L0CompactionConcurrency != nil {
		opts.Experimental.L0CompactionConcurrency = *cfg.L0CompactionConcurrency
	} else {
		opts.Experimental.L0CompactionConcurrency = 1
	}
	if cfg.CompactionDebtConcurrency != nil {
		opts.Experimental.CompactionDebtConcurrency = *cfg.CompactionDebtConcurrency
	} else {
		opts.Experimental.CompactionDebtConcurrency = 1 << 28
	}

	// Log the resolved configuration
	log.Printf("Pebble config: data_dir=%s cache=%dMB max_open_files=%d read_only=%v",
		cfg.DataDir, cfg.CacheMB, cfg.Handles, cfg.ReadOnly)
	log.Printf("  MemTable: size=%dMB count=%d stop_writes_threshold=%d",
		memTableSize/(1024*1024), memTableCount, memTableStopWrites)
	log.Printf("  Compaction: max_concurrent=%d l0_threshold=%d l0_stop_writes=%d",
		maxConcurrentCompactions, l0CompactionThreshold, l0StopWritesThreshold)
	log.Printf("  WAL: bytes_per_sync=%dKB disabled=%v no_sync=%v",
		walBytesPerSync/1024, opts.DisableWAL, cfg.GetNoSync())
	log.Printf("  Bloom filter: %d bits", bloomBits)

	cleanup := func() {
		cache.Unref()
	}
	return opts, cleanup
}

// buildLevelOptions creates pebble.LevelOptions from config or uses go-ethereum defaults.
func buildLevelOptions(levels []LevelConfig, bloomBits int) []pebble.LevelOptions {
	if len(levels) > 0 {
		opts := make([]pebble.LevelOptions, len(levels))
		for i, l := range levels {
			opts[i] = pebble.LevelOptions{
				TargetFileSize: l.TargetFileSize,
			}
			if bloomBits > 0 && i < len(levels)-1 {
				opts[i].FilterPolicy = bloom.FilterPolicy(bloomBits)
			}
		}
		return opts
	}

	// Go-ethereum defaults: 7 levels with doubling target file sizes
	targetSizes := []int64{
		2 << 20,   // 2MB
		4 << 20,   // 4MB
		8 << 20,   // 8MB
		16 << 20,  // 16MB
		32 << 20,  // 32MB
		64 << 20,  // 64MB
		128 << 20, // 128MB
	}
	opts := make([]pebble.LevelOptions, len(targetSizes))
	for i, size := range targetSizes {
		opts[i] = pebble.LevelOptions{
			TargetFileSize: size,
		}
		// Bloom filter on levels 0-5, not on level 6
		if bloomBits > 0 && i < len(targetSizes)-1 {
			opts[i].FilterPolicy = bloom.FilterPolicy(bloomBits)
		}
	}
	return opts
}
