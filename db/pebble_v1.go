package db

import (
	"fmt"
	"io"
	"log"
	"runtime"
	"strings"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/bloom"
	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/metrics"
)

const v1MaxMemTableSize = 512 << 20 // 512MB

// openV1 opens a Pebble v1 database and wraps it in the version-agnostic DB
// interface.
func openV1(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) (DB, func(), error) {
	log.Printf("Opening database with Pebble v1")
	opts, cacheCleanup := buildV1Options(cfg)
	opts.EventListener = newV1Listener(flushTracker, writeStallTracker)

	database, err := pebble.Open(cfg.DataDir, opts)
	if err != nil {
		cacheCleanup()
		return nil, nil, fmt.Errorf("opening pebble v1 database: %w", err)
	}
	// pebble.Open ran EnsureDefaults on opts in place, so opts now reflects the
	// effective configuration (all defaults materialized).
	resolved := resolveV1Config(cfg, opts)

	cleanup := func() {
		if err := database.Close(); err != nil {
			log.Printf("error closing database: %v", err)
		}
		cacheCleanup()
	}
	return &v1DB{db: database, resolved: resolved}, cleanup, nil
}

// buildV1Options translates a BenchConfig into pebble (v1) Options. The returned
// cleanup function must be called to release the cache.
func buildV1Options(cfg *config.BenchConfig) (*pebble.Options, func()) {
	cacheSize := int64(cfg.CacheMB) * 1024 * 1024
	cache := pebble.NewCache(cacheSize)

	memTableCount := cfg.GetMemTableCount()
	var memTableSize int
	if cfg.MemTableSize != nil {
		memTableSize = *cfg.MemTableSize
	} else {
		memTableSize = int(cacheSize) / 2 / memTableCount
	}
	if memTableSize >= v1MaxMemTableSize {
		memTableSize = v1MaxMemTableSize - 1
	}

	// MemTableStopWritesThreshold is set to twice the maximum number of allowed
	// memtables to accommodate temporary spikes.
	memTableStopWrites := memTableCount * 2
	if cfg.MemTableStopWritesThreshold != nil {
		memTableStopWrites = *cfg.MemTableStopWritesThreshold
	}

	maxConcurrentCompactions := runtime.NumCPU()
	if cfg.MaxConcurrentCompactions != nil {
		maxConcurrentCompactions = *cfg.MaxConcurrentCompactions
	}

	l0CompactionThreshold := 2
	if cfg.L0CompactionThreshold != nil {
		l0CompactionThreshold = *cfg.L0CompactionThreshold
	}

	l0StopWritesThreshold := 12
	if cfg.L0StopWritesThreshold != nil {
		l0StopWritesThreshold = *cfg.L0StopWritesThreshold
	}

	bytesPerSync := 512 * 1024
	if cfg.BytesPerSync != nil {
		bytesPerSync = *cfg.BytesPerSync
	}

	walBytesPerSync := 512 * 1024
	if cfg.WALBytesPerSync != nil {
		walBytesPerSync = *cfg.WALBytesPerSync
	}

	bloomBits := 10
	if cfg.BloomFilterBits != nil {
		bloomBits = *cfg.BloomFilterBits
	}

	levels := buildV1LevelOptions(cfg.Levels, bloomBits)

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
		BytesPerSync:          bytesPerSync,
		WALBytesPerSync:       walBytesPerSync,
	}

	if cfg.DisableWAL != nil && *cfg.DisableWAL {
		opts.DisableWAL = true
	}

	if cfg.ReadSamplingMultiplier != nil {
		opts.Experimental.ReadSamplingMultiplier = *cfg.ReadSamplingMultiplier
	} else {
		opts.Experimental.ReadSamplingMultiplier = -1
	}
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

	// Log the resolved configuration.
	log.Printf("Pebble v1 config: data_dir=%s cache=%dMB max_open_files=%d read_only=%v",
		cfg.DataDir, cfg.CacheMB, cfg.Handles, cfg.ReadOnly)
	log.Printf("  MemTable: size=%dMB count=%d stop_writes_threshold=%d",
		memTableSize/(1024*1024), memTableCount, memTableStopWrites)
	log.Printf("  Compaction: max_concurrent=%d l0_threshold=%d l0_stop_writes=%d",
		maxConcurrentCompactions, l0CompactionThreshold, l0StopWritesThreshold)
	log.Printf("  Sync: bytes_per_sync=%dKB wal_bytes_per_sync=%dKB",
		bytesPerSync/1024, walBytesPerSync/1024)
	log.Printf("  WAL: disabled=%v no_sync=%v",
		opts.DisableWAL, cfg.GetNoSync())
	log.Printf("  Bloom filter: %d bits", bloomBits)
	for i := range levels {
		l := &levels[i]
		filter := "none"
		if l.FilterPolicy != nil {
			filter = "bloom"
		}
		blockSize := "default"
		if l.BlockSize > 0 {
			blockSize = fmt.Sprintf("%dB", l.BlockSize)
		}
		log.Printf("  L%d: target_file_size=%dMB compression=%s block_size=%s filter=%s",
			i, l.TargetFileSize/(1024*1024), l.Compression, blockSize, filter)
	}

	cleanup := func() {
		cache.Unref()
	}
	return opts, cleanup
}

// buildV1LevelOptions builds the per-level v1 options by overlaying the per-level
// overrides from the config on top of the go-ethereum defaults. Levels and
// fields left unset inherit the default for that level.
func buildV1LevelOptions(levels []config.LevelConfig, bloomBits int) []pebble.LevelOptions {
	// The number of levels is the larger of the defaults and the overrides so
	// callers can both tweak existing levels and add deeper ones.
	n := max(len(defaultLevelTargetSizes), len(levels))

	opts := make([]pebble.LevelOptions, n)
	for i := range opts {
		// Start from the go-ethereum default for this level. Levels beyond the
		// defaults inherit the largest default target size.
		if i < len(defaultLevelTargetSizes) {
			opts[i].TargetFileSize = defaultLevelTargetSizes[i]
		} else {
			opts[i].TargetFileSize = defaultLevelTargetSizes[len(defaultLevelTargetSizes)-1]
		}
		// By default, apply the bloom filter on every level except the last.
		if bloomBits > 0 && i < n-1 {
			opts[i].FilterPolicy = bloom.FilterPolicy(bloomBits)
		}
		// Overlay the user-provided overrides for this level, if any.
		if i < len(levels) {
			applyV1LevelConfig(&opts[i], levels[i])
		}
	}
	return opts
}

// applyV1LevelConfig overlays the non-zero fields of a LevelConfig onto the given
// v1 LevelOptions, leaving the rest at their inherited defaults.
func applyV1LevelConfig(opt *pebble.LevelOptions, l config.LevelConfig) {
	if l.TargetFileSize > 0 {
		opt.TargetFileSize = l.TargetFileSize
	}
	if l.BlockSize > 0 {
		opt.BlockSize = l.BlockSize
	}
	if l.BlockRestartInterval > 0 {
		opt.BlockRestartInterval = l.BlockRestartInterval
	}
	if l.BlockSizeThreshold > 0 {
		opt.BlockSizeThreshold = l.BlockSizeThreshold
	}
	if l.IndexBlockSize > 0 {
		opt.IndexBlockSize = l.IndexBlockSize
	}
	if l.Compression != "" {
		if c, ok := v1Compression(l.Compression); ok {
			opt.Compression = c
		}
	}
	// Filter overrides: NoFilter disables it entirely, otherwise a per-level
	// bloom-bits override replaces the default policy.
	switch {
	case l.NoFilter:
		opt.FilterPolicy = nil
	case l.BloomFilterBits != nil:
		if *l.BloomFilterBits > 0 {
			opt.FilterPolicy = bloom.FilterPolicy(*l.BloomFilterBits)
		} else {
			opt.FilterPolicy = nil
		}
	}
}

// v1Compression maps a config compression string to a pebble (v1) Compression
// value. The boolean result reports whether the string was recognised.
func v1Compression(s string) (pebble.Compression, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default":
		return pebble.DefaultCompression, true
	case "none", "no", "nocompression":
		return pebble.NoCompression, true
	case "snappy":
		return pebble.SnappyCompression, true
	case "zstd":
		return pebble.ZstdCompression, true
	default:
		return pebble.DefaultCompression, false
	}
}

// resolveV1Config reads the effective configuration back from the (defaults-
// applied) v1 options.
func resolveV1Config(cfg *config.BenchConfig, opts *pebble.Options) *metrics.ResolvedConfig {
	rc := &metrics.ResolvedConfig{
		PebbleVersion:               "v1",
		DataDir:                     cfg.DataDir,
		CacheMB:                     cfg.CacheMB,
		MaxOpenFiles:                opts.MaxOpenFiles,
		ReadOnly:                    opts.ReadOnly,
		NoSync:                      cfg.GetNoSync(),
		DisableWAL:                  opts.DisableWAL,
		MemTableSize:                opts.MemTableSize,
		MemTableStopWritesThreshold: opts.MemTableStopWritesThreshold,
		L0CompactionThreshold:       opts.L0CompactionThreshold,
		L0StopWritesThreshold:       opts.L0StopWritesThreshold,
		L0CompactionConcurrency:     opts.Experimental.L0CompactionConcurrency,
		CompactionDebtConcurrency:   opts.Experimental.CompactionDebtConcurrency,
		ReadSamplingMultiplier:      opts.Experimental.ReadSamplingMultiplier,
		BytesPerSync:                opts.BytesPerSync,
		WALBytesPerSync:             opts.WALBytesPerSync,
	}
	if opts.MaxConcurrentCompactions != nil {
		rc.MaxConcurrentCompactions = opts.MaxConcurrentCompactions()
	}
	for i := range opts.Levels {
		l := &opts.Levels[i]
		filter := "none"
		if fp, ok := l.FilterPolicy.(bloom.FilterPolicy); ok {
			filter = fmt.Sprintf("bloom(%d)", int(fp))
		} else if l.FilterPolicy != nil {
			filter = "set"
		}
		rc.Levels = append(rc.Levels, metrics.ResolvedLevel{
			Level:                i,
			TargetFileSize:       l.TargetFileSize,
			Compression:          normalizeCompression(l.Compression.String()),
			BlockSize:            l.BlockSize,
			BlockRestartInterval: l.BlockRestartInterval,
			BlockSizeThreshold:   l.BlockSizeThreshold,
			IndexBlockSize:       l.IndexBlockSize,
			FilterPolicy:         filter,
		})
	}
	return rc
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
	db       *pebble.DB
	resolved *metrics.ResolvedConfig
}

func (d *v1DB) ResolvedConfig() *metrics.ResolvedConfig { return d.resolved }

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
	total := m.Total()
	out := &metrics.DBMetrics{
		DiskSpaceUsage:    m.DiskSpaceUsage(),
		ReadAmp:           int(m.ReadAmp()),
		WriteAmp:          total.WriteAmp(),
		BytesWritten:      total.BytesFlushed + total.BytesCompacted,
		BytesRead:         total.BytesRead,
		BytesIn:           total.BytesIn,
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
