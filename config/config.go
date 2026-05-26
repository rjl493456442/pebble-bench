package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// BenchConfig holds all configuration for the benchmark tool.
type BenchConfig struct {
	// Database settings
	DataDir  string `yaml:"data_dir"`
	CacheMB  int    `yaml:"cache_mb"`
	Handles  int    `yaml:"max_open_files"`
	ReadOnly bool   `yaml:"read_only"`

	// PebbleV2 selects the Pebble v2 backend instead of the default v1.
	PebbleV2 bool `yaml:"pebble_v2"`

	// MemTable settings
	MemTableSize                *int `yaml:"mem_table_size"`
	MemTableCount               *int `yaml:"mem_table_count"`
	MemTableStopWritesThreshold *int `yaml:"mem_table_stop_writes_threshold"`

	// Compaction settings
	MaxConcurrentCompactions  *int    `yaml:"max_concurrent_compactions"`
	L0CompactionThreshold     *int    `yaml:"l0_compaction_threshold"`
	L0StopWritesThreshold     *int    `yaml:"l0_stop_writes_threshold"`
	L0CompactionConcurrency   *int    `yaml:"l0_compaction_concurrency"`
	CompactionDebtConcurrency *uint64 `yaml:"compaction_debt_concurrency"`
	ReadSamplingMultiplier    *int64  `yaml:"read_sampling_multiplier"`

	// Sync settings
	BytesPerSync    *int  `yaml:"bytes_per_sync"`
	WALBytesPerSync *int  `yaml:"wal_bytes_per_sync"`
	DisableWAL      *bool `yaml:"disable_wal"`
	NoSync          *bool `yaml:"no_sync"`

	// Bloom filter
	BloomFilterBits *int `yaml:"bloom_filter_bits"`

	// Level settings (if empty, use go-ethereum defaults)
	Levels []LevelConfig `yaml:"levels"`

	// Benchmark settings
	Benchmark BenchmarkConfig `yaml:"benchmark"`
}

// LevelConfig defines per-level options. Any field left at its zero value
// inherits the go-ethereum / pebble default for that level, so a level entry
// only needs to specify the options it wants to override.
type LevelConfig struct {
	// TargetFileSize is the target SSTable size for the level, in bytes.
	TargetFileSize int64 `yaml:"target_file_size"`

	// Compression selects the per-block compression algorithm. One of
	// "default", "none", "snappy" or "zstd" (case-insensitive). Empty keeps
	// the pebble default (snappy).
	Compression string `yaml:"compression"`

	// BlockSize is the target uncompressed size of each data block, in bytes
	// (pebble default: 4096).
	BlockSize int `yaml:"block_size"`

	// BlockRestartInterval is the number of keys between restart points for
	// delta encoding of keys (pebble default: 16).
	BlockRestartInterval int `yaml:"block_restart_interval"`

	// BlockSizeThreshold finishes a block once it grows past this percentage
	// of the target block size (pebble default: 90).
	BlockSizeThreshold int `yaml:"block_size_threshold"`

	// IndexBlockSize is the target uncompressed size of each index block, in
	// bytes (pebble default: equal to BlockSize).
	IndexBlockSize int `yaml:"index_block_size"`

	// NoFilter disables the bloom filter for this level, overriding the
	// default policy (bloom on every level except the last).
	NoFilter bool `yaml:"no_filter"`

	// BloomFilterBits overrides the number of bloom filter bits per key for
	// this level. When nil, the database-wide bloom_filter_bits is used.
	BloomFilterBits *int `yaml:"bloom_filter_bits"`
}

// BenchmarkConfig holds benchmark-specific parameters.
type BenchmarkConfig struct {
	Name        string        `yaml:"name"`
	Duration    time.Duration `yaml:"duration"`
	Concurrency int           `yaml:"concurrency"`
	NumOps      uint64        `yaml:"num_ops"`
	KeySize     int           `yaml:"key_size"`
	ValueSize   int           `yaml:"value_size"`

	// Batch-specific
	BatchSize int `yaml:"batch_size"`

	// Mixed-specific
	ReadPercent int `yaml:"read_percent"`

	// Dataset initialization
	InitTargetSize string `yaml:"init_target_size"`
}

// intPtr returns a pointer to the given int.
func intPtr(v int) *int { return &v }

// int64Ptr returns a pointer to the given int64.
func int64Ptr(v int64) *int64 { return &v }

// uint64Ptr returns a pointer to the given uint64.
func uint64Ptr(v uint64) *uint64 { return &v }

// boolPtr returns a pointer to the given bool.
func boolPtr(v bool) *bool { return &v }

// DefaultConfig returns a BenchConfig with go-ethereum inspired defaults.
func DefaultConfig() *BenchConfig {
	return &BenchConfig{
		DataDir:  "/tmp/pebble-bench",
		CacheMB:  2048,
		Handles:  40960,
		ReadOnly: false,

		MemTableCount:               intPtr(4),
		MemTableStopWritesThreshold: intPtr(8),

		MaxConcurrentCompactions:  nil, // defaults to runtime.NumCPU in pebble.go
		L0CompactionThreshold:     intPtr(2),
		L0StopWritesThreshold:     intPtr(12),
		L0CompactionConcurrency:   intPtr(1),
		CompactionDebtConcurrency: uint64Ptr(1 << 28), // 256MB
		ReadSamplingMultiplier:    int64Ptr(-1),

		WALBytesPerSync: intPtr(512 * 1024), // 500KB
		DisableWAL:      boolPtr(false),
		NoSync:          boolPtr(true),

		BloomFilterBits: intPtr(10),

		Benchmark: BenchmarkConfig{
			Name:        "read",
			Duration:    5 * time.Minute,
			Concurrency: 4,
			KeySize:     32,
			ValueSize:   128,
			BatchSize:   100,
			ReadPercent: 80,
		},
	}
}

// LoadConfig loads a BenchConfig from a YAML file, merging with defaults.
func LoadConfig(path string) (*BenchConfig, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}
	return cfg, nil
}

// Validate checks the configuration for invalid values, returning the first
// error encountered.
func (c *BenchConfig) Validate() error {
	for i, l := range c.Levels {
		if !validCompression(l.Compression) {
			return fmt.Errorf("levels[%d]: invalid compression %q (want one of: default, none, snappy, zstd)", i, l.Compression)
		}
		if l.BloomFilterBits != nil && *l.BloomFilterBits < 0 {
			return fmt.Errorf("levels[%d]: bloom_filter_bits must not be negative", i)
		}
	}
	return nil
}

// validCompression reports whether s is a recognised compression name. The
// actual mapping to a Pebble compression value is version-specific and lives in
// the db package; here we only validate the user-facing string so the config
// package stays independent of any Pebble release.
func validCompression(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default", "none", "no", "nocompression", "snappy", "zstd":
		return true
	default:
		return false
	}
}

// GetMemTableCount returns the configured memtable count or the default.
func (c *BenchConfig) GetMemTableCount() int {
	if c.MemTableCount != nil {
		return *c.MemTableCount
	}
	return 4
}

// GetNoSync returns whether async writes are enabled.
func (c *BenchConfig) GetNoSync() bool {
	if c.NoSync != nil {
		return *c.NoSync
	}
	return true
}

