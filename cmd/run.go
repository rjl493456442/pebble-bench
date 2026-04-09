package cmd

import (
	"fmt"
	"log"
	"time"

	"github.com/rjl493456442/pebble-bench/bench"
	"github.com/rjl493456442/pebble-bench/datagen"
	"github.com/rjl493456442/pebble-bench/db"
	"github.com/rjl493456442/pebble-bench/metrics"
	"github.com/urfave/cli/v2"
)

// Run-specific flags.
var (
	benchmarkFlag = &cli.StringFlag{
		Name:    "benchmark",
		Aliases: []string{"b"},
		Usage:   "benchmark name (overrides config)",
	}
	durationFlag = &cli.DurationFlag{
		Name:    "duration",
		Aliases: []string{"d"},
		Usage:   "benchmark duration (e.g., 5m, 30s)",
	}
	concurrencyFlag = &cli.IntFlag{
		Name:  "concurrency",
		Usage: "number of worker goroutines",
	}
	numOpsFlag = &cli.Uint64Flag{
		Name:  "num-ops",
		Usage: "maximum number of operations",
	}
	outputFlag = &cli.StringFlag{
		Name:  "output",
		Value: "terminal",
		Usage: "output format: terminal, json, markdown",
	}
	outputFileFlag = &cli.StringFlag{
		Name:  "output-file",
		Usage: "write results to file (format inferred from --output flag)",
	}
)

func runCommand() *cli.Command {
	return &cli.Command{
		Name:  "run",
		Usage: "Run a benchmark",
		Description: `Execute a benchmark scenario against the initialized dataset.

Available benchmarks:
  scan      - Iterate over keys using Pebble iterator
  read      - Read keys in random order
  write     - Batched key-value writes
  mixed     - Mixed read/write workload`,
		Flags: append(sharedFlags(),
			benchmarkFlag,
			durationFlag,
			concurrencyFlag,
			numOpsFlag,
			outputFlag,
			outputFileFlag,
		),
		Action: runBenchmark,
	}
}

func runBenchmark(c *cli.Context) error {
	closeLog := setupLogFile(c)
	defer closeLog()

	cfg, err := loadConfig(c)
	if err != nil {
		return err
	}

	// Apply CLI flag overrides
	if v := c.String(benchmarkFlag.Name); v != "" {
		cfg.Benchmark.Name = v
	}
	if v := c.Duration(durationFlag.Name); v > 0 {
		cfg.Benchmark.Duration = v
	}
	if v := c.Int(concurrencyFlag.Name); v > 0 {
		cfg.Benchmark.Concurrency = v
	}
	if v := c.Uint64(numOpsFlag.Name); v > 0 {
		cfg.Benchmark.NumOps = v
	}

	// Validate
	if cfg.Benchmark.Name == "" {
		return fmt.Errorf("benchmark name must be specified via --benchmark or config file")
	}
	if cfg.Benchmark.Duration == 0 && cfg.Benchmark.NumOps == 0 {
		return fmt.Errorf("either duration or num-ops must be specified")
	}

	// Load dataset metadata
	meta, err := datagen.LoadMeta(cfg.DataDir)
	if err != nil {
		// For write-only benchmarks, metadata is optional
		if cfg.Benchmark.Name == "write" {
			log.Printf("No dataset metadata found, using empty dataset for write benchmark")
			meta = &datagen.Meta{
				TotalKeys: 0,
				KeySize:   cfg.Benchmark.KeySize,
				ValueSize: cfg.Benchmark.ValueSize,
			}
		} else {
			return err
		}
	}

	log.Printf("Benchmark: name=%s, duration=%s, concurrency=%d",
		cfg.Benchmark.Name, cfg.Benchmark.Duration, cfg.Benchmark.Concurrency)
	log.Printf("Dataset: %d keys, key_size=%d, value_size=%d",
		meta.TotalKeys, meta.KeySize, meta.ValueSize)

	// Open database
	flushTracker := metrics.NewFlushTracker()
	writeStallTracker := metrics.NewWriteStallTracker()
	database, writeOpts, cleanup, err := db.Open(cfg, flushTracker, writeStallTracker)
	if err != nil {
		return err
	}
	defer cleanup()

	// Create metrics collector
	collector := metrics.NewCollector(database, 3*time.Second, flushTracker, writeStallTracker)

	// Execute benchmark
	result, err := bench.Execute(database, writeOpts, cfg, meta, collector)
	if err != nil {
		return err
	}

	// Output results
	metrics.PrintSummary(result)

	outputPath := c.String(outputFileFlag.Name)
	if outputPath != "" {
		switch c.String(outputFlag.Name) {
		case "json":
			if err := metrics.WriteJSON(outputPath, result); err != nil {
				return fmt.Errorf("writing JSON results: %w", err)
			}
		case "markdown", "md":
			if err := metrics.WriteMarkdown(outputPath, result); err != nil {
				return fmt.Errorf("writing Markdown results: %w", err)
			}
		default:
			if err := metrics.WriteJSON(outputPath, result); err != nil {
				return fmt.Errorf("writing results: %w", err)
			}
		}
		log.Printf("Results written to %s", outputPath)
	}
	return nil
}
