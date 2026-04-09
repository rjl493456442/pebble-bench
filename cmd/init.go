package cmd

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/rjl493456442/pebble-bench/datagen"
	"github.com/rjl493456442/pebble-bench/db"
	"github.com/rjl493456442/pebble-bench/metrics"
	"github.com/urfave/cli/v2"
)

// Init-specific flags.
var (
	targetSizeFlag = &cli.StringFlag{
		Name:  "target-size",
		Usage: "target dataset size (e.g., 1GB, 200GB)",
	}
	keySizeFlag = &cli.IntFlag{
		Name:  "key-size",
		Usage: "key size in bytes (default: from config)",
	}
	valueSizeFlag = &cli.IntFlag{
		Name:  "value-size",
		Usage: "value size in bytes (default: from config)",
	}
	batchSizeFlag = &cli.IntFlag{
		Name:  "batch-size",
		Value: 1000,
		Usage: "batch size for population",
	}
)

func initCommand() *cli.Command {
	return &cli.Command{
		Name:  "init",
		Usage: "Initialize a dataset for benchmarking",
		Description: `Populates a Pebble database with data to the specified target size.
The dataset uses deterministic key generation (sha256-based) so read
benchmarks can regenerate keys by index without storing them in memory.`,
		Flags: append(sharedFlags(),
			targetSizeFlag,
			keySizeFlag,
			valueSizeFlag,
			batchSizeFlag,
		),
		Action: runInit,
	}
}

func runInit(c *cli.Context) error {
	closeLog := setupLogFile(c)
	defer closeLog()

	cfg, err := loadConfig(c)
	if err != nil {
		return err
	}

	// Apply flag overrides
	if v := c.String(targetSizeFlag.Name); v != "" {
		cfg.Benchmark.InitTargetSize = v
	}
	if v := c.Int(keySizeFlag.Name); v > 0 {
		cfg.Benchmark.KeySize = v
	}
	if v := c.Int(valueSizeFlag.Name); v > 0 {
		cfg.Benchmark.ValueSize = v
	}
	batchSize := c.Int(batchSizeFlag.Name)

	targetBytes, err := parseSize(cfg.Benchmark.InitTargetSize)
	if err != nil {
		return fmt.Errorf("parsing target size: %w", err)
	}
	if targetBytes == 0 {
		return fmt.Errorf("target size must be specified via --target-size or config file")
	}

	log.Printf("Initializing dataset: target=%s, key_size=%d, value_size=%d",
		cfg.Benchmark.InitTargetSize, cfg.Benchmark.KeySize, cfg.Benchmark.ValueSize)

	flushTracker := metrics.NewFlushTracker()
	writeStallTracker := metrics.NewWriteStallTracker()
	database, writeOpts, cleanup, err := db.OpenForInit(cfg, flushTracker, writeStallTracker)
	if err != nil {
		return err
	}
	defer cleanup()

	meta, err := datagen.Populate(database, targetBytes, cfg.Benchmark.KeySize, cfg.Benchmark.ValueSize, batchSize, writeOpts)
	if err != nil {
		return err
	}

	if err := datagen.SaveMeta(cfg.DataDir, meta); err != nil {
		return fmt.Errorf("saving metadata: %w", err)
	}

	log.Printf("Dataset initialized: %d keys, metadata saved to %s/bench_meta.json", meta.TotalKeys, cfg.DataDir)
	return nil
}

// parseSize parses a human-readable size string like "200GB", "1GB", "512MB".
func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}

	s = strings.TrimSpace(s)
	s = strings.ToUpper(s)

	multiplier := int64(1)
	numStr := s

	switch {
	case strings.HasSuffix(s, "TB"):
		multiplier = 1 << 40
		numStr = s[:len(s)-2]
	case strings.HasSuffix(s, "GB"):
		multiplier = 1 << 30
		numStr = s[:len(s)-2]
	case strings.HasSuffix(s, "MB"):
		multiplier = 1 << 20
		numStr = s[:len(s)-2]
	case strings.HasSuffix(s, "KB"):
		multiplier = 1 << 10
		numStr = s[:len(s)-2]
	case strings.HasSuffix(s, "B"):
		numStr = s[:len(s)-1]
	}

	numStr = strings.TrimSpace(numStr)

	// Try float first for things like "1.5GB"
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}

	return int64(val * float64(multiplier)), nil
}
