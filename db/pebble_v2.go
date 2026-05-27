package db

import (
	"fmt"
	"io"
	"log"
	"runtime"
	"strings"

	pebble "github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
	"github.com/cockroachdb/pebble/v2/sstable"
	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/metrics"
)

const v2MaxMemTableSize = 512 << 20 // 512MB

// openV2 opens a Pebble v2 database and wraps it in the version-agnostic DB
// interface.
func openV2(cfg *config.BenchConfig, flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker, syncTracker *metrics.SyncTracker, readTracker *metrics.ReadTracker) (DB, func(), error) {
	log.Printf("Opening database with Pebble v2")
	opts, cacheCleanup := buildV2Options(cfg)
	opts.EventListener = newV2Listener(flushTracker, writeStallTracker)
	opts.FS = instrumentV2FS(opts.FS, syncTracker, readTracker)

	database, err := pebble.Open(cfg.DataDir, opts)
	if err != nil {
		cacheCleanup()
		return nil, nil, fmt.Errorf("opening pebble v2 database: %w", err)
	}
	// Unlike v1, v2's Open clones the options before applying defaults, so our
	// pointer is left untouched. Apply defaults ourselves (idempotent) to read
	// back the effective per-level configuration.
	opts.EnsureDefaults()
	resolved := resolveV2Config(cfg, opts)

	cleanup := func() {
		if err := database.Close(); err != nil {
			log.Printf("error closing database: %v", err)
		}
		cacheCleanup()
	}
	return &v2DB{db: database, resolved: resolved}, cleanup, nil
}

// resolveV2Config reads the effective configuration back from the (defaults-
// applied) v2 options.
func resolveV2Config(cfg *config.BenchConfig, opts *pebble.Options) *metrics.ResolvedConfig {
	rc := &metrics.ResolvedConfig{
		PebbleVersion:               "v2",
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
	if opts.CompactionConcurrencyRange != nil {
		// Record the upper bound, comparable to v1's MaxConcurrentCompactions.
		_, upper := opts.CompactionConcurrencyRange()
		rc.MaxConcurrentCompactions = upper
	}
	for i := range opts.Levels {
		l := &opts.Levels[i]
		filter := "none"
		if fp, ok := l.FilterPolicy.(bloom.FilterPolicy); ok {
			filter = fmt.Sprintf("bloom(%d)", int(fp))
		} else if l.FilterPolicy != nil {
			filter = "set"
		}
		compression := "default"
		if l.Compression != nil {
			compression = normalizeCompression(l.Compression().Name)
		}
		rc.Levels = append(rc.Levels, metrics.ResolvedLevel{
			Level:                i,
			TargetFileSize:       opts.TargetFileSizes[i],
			Compression:          compression,
			BlockSize:            l.BlockSize,
			BlockRestartInterval: l.BlockRestartInterval,
			BlockSizeThreshold:   l.BlockSizeThreshold,
			IndexBlockSize:       l.IndexBlockSize,
			FilterPolicy:         filter,
		})
	}
	return rc
}

// buildV2Options translates a BenchConfig into pebble/v2 Options. It mirrors
// config.BuildPebbleOptions (the v1 builder) but targets the v2 API, whose
// Levels, compression and compaction-concurrency knobs differ. The returned
// cleanup function must be called to release the cache.
func buildV2Options(cfg *config.BenchConfig) (*pebble.Options, func()) {
	cacheSize := int64(cfg.CacheMB) * 1024 * 1024
	cache := pebble.NewCache(cacheSize)

	memTableCount := cfg.GetMemTableCount()
	var memTableSize int
	if cfg.MemTableSize != nil {
		memTableSize = *cfg.MemTableSize
	} else {
		memTableSize = int(cacheSize) / 2 / memTableCount
	}
	if memTableSize >= v2MaxMemTableSize {
		memTableSize = v2MaxMemTableSize - 1
	}

	memTableStopWrites := memTableCount * 2
	if cfg.MemTableStopWritesThreshold != nil {
		memTableStopWrites = *cfg.MemTableStopWritesThreshold
	}

	maxConcurrentCompactions := runtime.NumCPU()
	if cfg.MaxConcurrentCompactions != nil {
		maxConcurrentCompactions = *cfg.MaxConcurrentCompactions
	}
	if maxConcurrentCompactions < 1 {
		maxConcurrentCompactions = 1
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

	opts := &pebble.Options{
		Cache:                       cache,
		MaxOpenFiles:                cfg.Handles,
		MemTableSize:                uint64(memTableSize),
		MemTableStopWritesThreshold: memTableStopWrites,

		// v2 replaces MaxConcurrentCompactions with a [lower, upper] range.
		CompactionConcurrencyRange: func() (int, int) {
			return 1, maxConcurrentCompactions
		},
		L0CompactionThreshold: l0CompactionThreshold,
		L0StopWritesThreshold: l0StopWritesThreshold,
		ReadOnly:              cfg.ReadOnly,
		BytesPerSync:          bytesPerSync,
		WALBytesPerSync:       walBytesPerSync,
	}

	// Build the per-level options in place (v2 uses fixed-size arrays and keeps
	// the per-level target file size in a separate Options.TargetFileSizes).
	buildV2LevelOptions(opts, cfg.Levels, bloomBits)

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
	log.Printf("Pebble v2 config: data_dir=%s cache=%dMB max_open_files=%d read_only=%v",
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
	for i := range opts.Levels {
		l := &opts.Levels[i]
		filter := "none"
		if l.FilterPolicy != nil {
			filter = "bloom"
		}
		blockSize := "default"
		if l.BlockSize > 0 {
			blockSize = fmt.Sprintf("%dB", l.BlockSize)
		}
		compression := "default"
		if l.Compression != nil {
			compression = l.Compression().Name
		}
		log.Printf("  L%d: target_file_size=%dMB compression=%s block_size=%s filter=%s",
			i, opts.TargetFileSizes[i]/(1024*1024), compression, blockSize, filter)
	}

	cleanup := func() {
		cache.Unref()
	}
	return opts, cleanup
}

// buildV2LevelOptions overlays the per-level config overrides on top of the
// go-ethereum defaults. In v2 the per-level target file size lives in the
// separate Options.TargetFileSizes array, while the remaining per-level knobs
// live in Options.Levels.
func buildV2LevelOptions(opts *pebble.Options, overrides []config.LevelConfig, bloomBits int) {
	n := len(opts.Levels)
	for i := 0; i < n; i++ {
		opts.TargetFileSizes[i] = defaultLevelTargetSizes[i]
		// Apply the bloom filter on every level except the last by default.
		if bloomBits > 0 && i < n-1 {
			opts.Levels[i].FilterPolicy = bloom.FilterPolicy(bloomBits)
		}
		if i < len(overrides) {
			if overrides[i].TargetFileSize > 0 {
				opts.TargetFileSizes[i] = overrides[i].TargetFileSize
			}
			applyV2LevelConfig(&opts.Levels[i], overrides[i])
		}
		// v1 defaults every unspecified level to snappy independently, whereas
		// v2 would otherwise inherit the previous level's compression. Pin
		// snappy here so the same config yields the same effective compression
		// on both backends (keeping v1/v2 comparisons fair).
		if opts.Levels[i].Compression == nil {
			opts.Levels[i].Compression = func() *sstable.CompressionProfile { return sstable.SnappyCompression }
		}
	}
}

// applyV2LevelConfig overlays the non-zero fields of a LevelConfig onto the
// given v2 LevelOptions. The target file size is handled by the caller since
// v2 stores it outside of LevelOptions.
func applyV2LevelConfig(opt *pebble.LevelOptions, l config.LevelConfig) {
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
		if profile, ok := v2Compression(l.Compression); ok {
			opt.Compression = func() *sstable.CompressionProfile { return profile }
		}
	}
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

// v2Compression maps a config compression string to a v2 compression profile.
// The boolean result reports whether the string was recognised.
func v2Compression(s string) (*sstable.CompressionProfile, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default":
		return sstable.DefaultCompression, true
	case "none", "no", "nocompression":
		return sstable.NoCompression, true
	case "snappy":
		return sstable.SnappyCompression, true
	case "zstd":
		return sstable.ZstdCompression, true
	default:
		return sstable.DefaultCompression, false
	}
}

// newV2Listener builds the event listener for a Pebble v2 database.
func newV2Listener(flushTracker *metrics.FlushTracker, writeStallTracker *metrics.WriteStallTracker) *pebble.EventListener {
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

// v2DB adapts *pebble.DB (v2) to the DB interface.
type v2DB struct {
	db       *pebble.DB
	resolved *metrics.ResolvedConfig
}

func (d *v2DB) ResolvedConfig() *metrics.ResolvedConfig { return d.resolved }

func (d *v2DB) NewBatch() Batch { return &v2Batch{batch: d.db.NewBatch()} }

func (d *v2DB) Get(key []byte) ([]byte, io.Closer, error) {
	value, closer, err := d.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil, ErrNotFound
	}
	return value, closer, err
}

func (d *v2DB) NewIter() (Iterator, error) {
	iter, err := d.db.NewIter(&pebble.IterOptions{})
	if err != nil {
		return nil, err
	}
	return iter, nil
}

func (d *v2DB) Flush() error { return d.db.Flush() }

func (d *v2DB) Close() error { return d.db.Close() }

func (d *v2DB) Metrics() *metrics.DBMetrics {
	m := d.db.Metrics()
	total := m.Total()
	out := &metrics.DBMetrics{
		DiskSpaceUsage: m.DiskSpaceUsage(),
		ReadAmp:        int(m.ReadAmp()),
		WriteAmp:       total.WriteAmp(),
		// v2 tracks sstable and blob bytes separately.
		BytesWritten:      total.TableBytesFlushed + total.TableBytesCompacted + total.BlobBytesFlushed + total.BlobBytesCompacted,
		BytesRead:         total.TableBytesRead,
		BytesIn:           total.TableBytesIn,
		CompactionCount:   m.Compact.Count,
		CompactionDebt:    m.Compact.EstimatedDebt,
		CompactionsActive: m.Compact.NumInProgress,
		MemTableSize:      m.MemTable.Size,
		MemTableCount:     m.MemTable.Count,
		BlockCacheHits:    m.BlockCache.Hits,
		BlockCacheMisses:  m.BlockCache.Misses,
		// v2 renamed the table cache to the file cache.
		TableCacheHits:   m.FileCache.Hits,
		TableCacheMisses: m.FileCache.Misses,
		FilterHits:       m.Filter.Hits,
		FilterMisses:     m.Filter.Misses,
	}
	for i, l := range m.Levels {
		if i < len(out.LevelSizes) {
			// v2 renamed Size/NumFiles to TablesSize/TablesCount.
			out.LevelSizes[i] = l.TablesSize
			out.LevelFiles[i] = l.TablesCount
		}
	}
	return out
}

// v2Batch adapts *pebble.Batch (v2) to the Batch interface.
type v2Batch struct {
	batch *pebble.Batch
}

func (b *v2Batch) Set(key, value []byte) error { return b.batch.Set(key, value, nil) }

func (b *v2Batch) Commit(sync bool) error {
	opts := pebble.NoSync
	if sync {
		opts = pebble.Sync
	}
	return b.batch.Commit(opts)
}

func (b *v2Batch) Close() error { return b.batch.Close() }
