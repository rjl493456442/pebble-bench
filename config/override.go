package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ApplyOverrides parses key=value override strings and applies them to the config.
func ApplyOverrides(cfg *BenchConfig, overrides []string) error {
	for _, o := range overrides {
		parts := strings.SplitN(o, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid override format %q, expected key=value", o)
		}
		key, value := parts[0], parts[1]

		if err := applyOverride(cfg, key, value); err != nil {
			return fmt.Errorf("applying override %q: %w", o, err)
		}
	}
	return nil
}

func applyOverride(cfg *BenchConfig, key, value string) error {
	// Per-level target file size: target_file_size_l<N>, e.g. target_file_size_l0=4MB.
	// Handled before the switch so the level index can be parsed out.
	if strings.HasPrefix(key, "target_file_size_l") {
		idxStr := strings.TrimPrefix(key, "target_file_size_l")
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return fmt.Errorf("invalid level index in %q: %w", key, err)
		}
		if idx < 0 || idx > 6 {
			return fmt.Errorf("level index in %q out of range [0,6]", key)
		}
		size, err := parseSize(value)
		if err != nil {
			return fmt.Errorf("invalid size for %q: %w", key, err)
		}
		// Grow the Levels slice to cover this level if necessary, preserving any
		// already-set per-level overrides at lower indices.
		for len(cfg.Levels) <= idx {
			cfg.Levels = append(cfg.Levels, LevelConfig{})
		}
		cfg.Levels[idx].TargetFileSize = size
		return nil
	}

	switch key {
	// Database settings
	case "data_dir":
		cfg.DataDir = value
	case "cache_mb":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.CacheMB = v
	case "max_open_files":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.Handles = v
	case "pebble_v2":
		v, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		cfg.PebbleV2 = v

	// MemTable settings
	case "mem_table_size":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.MemTableSize = &v
	case "mem_table_count":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.MemTableCount = &v
	case "mem_table_stop_writes_threshold":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.MemTableStopWritesThreshold = &v

	// Compaction settings
	case "max_concurrent_compactions":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.MaxConcurrentCompactions = &v
	case "l0_compaction_threshold":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.L0CompactionThreshold = &v
	case "l0_stop_writes_threshold":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.L0StopWritesThreshold = &v
	case "l0_compaction_concurrency":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.L0CompactionConcurrency = &v
	case "compaction_debt_concurrency":
		v, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		cfg.CompactionDebtConcurrency = &v
	case "read_sampling_multiplier":
		v, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		cfg.ReadSamplingMultiplier = &v
	case "l_base_max_bytes":
		v, err := parseSize(value)
		if err != nil {
			return err
		}
		cfg.LBaseMaxBytes = &v
	case "level_multiplier":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.LevelMultiplier = &v

	// Sync settings
	case "bytes_per_sync":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.BytesPerSync = &v
	case "wal_bytes_per_sync":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.WALBytesPerSync = &v
	case "disable_wal":
		v, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		cfg.DisableWAL = &v
	case "no_sync":
		v, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		cfg.NoSync = &v

	// Bloom filter
	case "bloom_filter_bits":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.BloomFilterBits = &v

	// Benchmark settings
	case "benchmark.name":
		cfg.Benchmark.Name = value
	case "benchmark.duration":
		v, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		cfg.Benchmark.Duration = v
	case "benchmark.concurrency":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.Benchmark.Concurrency = v
	case "benchmark.num_ops":
		v, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		cfg.Benchmark.NumOps = v
	case "benchmark.key_size":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.Benchmark.KeySize = v
	case "benchmark.value_size":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.Benchmark.ValueSize = v
	case "benchmark.batch_size":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.Benchmark.BatchSize = v
	case "benchmark.read_percent":
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.Benchmark.ReadPercent = v
	case "benchmark.init_target_size":
		cfg.Benchmark.InitTargetSize = value

	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// ListOverrideKeys returns all supported override keys for help text.
func ListOverrideKeys() []string {
	return []string{
		"data_dir", "cache_mb", "max_open_files", "pebble_v2",
		"mem_table_size", "mem_table_count", "mem_table_stop_writes_threshold",
		"max_concurrent_compactions", "l0_compaction_threshold",
		"l0_stop_writes_threshold", "l0_compaction_concurrency",
		"compaction_debt_concurrency", "read_sampling_multiplier",
		"l_base_max_bytes", "level_multiplier",
		"target_file_size_l0", "target_file_size_l1", "target_file_size_l2",
		"target_file_size_l3", "target_file_size_l4", "target_file_size_l5",
		"target_file_size_l6",
		"bytes_per_sync", "wal_bytes_per_sync", "disable_wal", "no_sync",
		"bloom_filter_bits",
		"benchmark.name", "benchmark.duration", "benchmark.concurrency",
		"benchmark.num_ops", "benchmark.key_size", "benchmark.value_size",
		"benchmark.batch_size", "benchmark.read_percent",
		"benchmark.init_target_size",
	}
}

// parseSize parses a byte size with an optional unit suffix (B/KB/MB/GB/TB,
// case-insensitive, no space). Plain integers are treated as bytes. This lets
// override values be written compactly, e.g. l_base_max_bytes=256MB.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	upper := strings.ToUpper(s)
	var mult int64 = 1
	switch {
	case strings.HasSuffix(upper, "TB"):
		mult = 1 << 40
		s = s[:len(s)-2]
	case strings.HasSuffix(upper, "GB"):
		mult = 1 << 30
		s = s[:len(s)-2]
	case strings.HasSuffix(upper, "MB"):
		mult = 1 << 20
		s = s[:len(s)-2]
	case strings.HasSuffix(upper, "KB"):
		mult = 1 << 10
		s = s[:len(s)-2]
	case strings.HasSuffix(upper, "B"):
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, err
	}
	return n * mult, nil
}
