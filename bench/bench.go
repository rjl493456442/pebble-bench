package bench

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/datagen"
	"github.com/rjl493456442/pebble-bench/db"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// Benchmark defines the interface for all benchmark scenarios.
type Benchmark interface {
	// Name returns the benchmark name.
	Name() string

	// Setup initializes the benchmark state. sync reports whether writes should
	// be committed synchronously.
	Setup(database db.DB, sync bool, cfg *config.BenchmarkConfig, meta *datagen.Meta) error

	// Run executes the benchmark workload in a single worker goroutine. It should
	// loop until ctx is cancelled, recording latencies into the registry's
	// per-operation histograms (obtained via reg.Get).
	Run(ctx context.Context, workerID int, reg *metrics.HistogramRegistry) error
}

// Registry maps benchmark names to constructor functions.
var Registry = map[string]func() Benchmark{
	"scan":  func() Benchmark { return &Scan{} },
	"read":  func() Benchmark { return &Read{} },
	"write": func() Benchmark { return &Write{} },
	"mixed": func() Benchmark { return &Mixed{} },
}

// Execute runs a benchmark with the given configuration.
func Execute(database db.DB, syncWrites bool, cfg *config.BenchConfig, meta *datagen.Meta, collector *metrics.Collector) (*metrics.Result, error) {
	benchCfg := cfg.Benchmark
	constructor, ok := Registry[benchCfg.Name]
	if !ok {
		return nil, fmt.Errorf("unknown benchmark: %s (available: %v)", benchCfg.Name, availableBenchmarks())
	}

	// Construct the benchmark
	b := constructor()
	if err := b.Setup(database, syncWrites, &benchCfg, meta); err != nil {
		return nil, fmt.Errorf("setup: %w", err)
	}

	// Create context with timeout or ops limit
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if benchCfg.Duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, benchCfg.Duration)
		defer cancel()
	}

	// Start metrics collector
	collectorCtx, collectorCancel := context.WithCancel(context.Background())
	defer collectorCancel()
	go collector.Run(collectorCtx)

	// Start benchmark workers
	var (
		wg        sync.WaitGroup
		startTime = time.Now()
		reg       = metrics.NewHistogramRegistry()
		opsCount  atomic.Int64
		maxOps    = int64(benchCfg.NumOps)
	)
	concurrency := benchCfg.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	log.Printf("Starting benchmark %q with %d workers for %s", b.Name(), concurrency, benchCfg.Duration)

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			wrappedCtx := ctx
			if maxOps > 0 {
				// Wrap to check ops limit
				wrappedCtx = context.WithValue(ctx, opsCountKey{}, &opsCounter{
					count:  &opsCount,
					max:    maxOps,
					cancel: cancel,
				})
			}
			if err := b.Run(wrappedCtx, workerID, reg); err != nil && ctx.Err() == nil {
				log.Printf("Worker %d error: %v", workerID, err)
			}
		}(i)
	}

	// Print periodic stats and collect tick records
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	var tickRecords []metrics.TickRecord
	var tickMu sync.Mutex

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ticks := reg.Tick()
				for _, ot := range ticks {
					metrics.PrintTick(os.Stdout, ot)
				}
				// Combine per-operation ticks into a single total for the chart
				// (and print it too when there is more than one op type).
				t := metrics.MergeTicks(ticks, "total")
				if len(ticks) > 1 {
					metrics.PrintTick(os.Stdout, t)
				}
				if t.Hist.TotalCount() > 0 {
					tickMu.Lock()
					tickRecords = append(tickRecords, metrics.TickRecord{
						Elapsed:   t.Elapsed.Seconds(),
						OpsPerSec: t.IntervalOpsPerSec(),
						P50Us:     t.Hist.ValueAtPercentile(50) / int64(time.Microsecond),
						P99Us:     t.Hist.ValueAtPercentile(99) / int64(time.Microsecond),
					})
					tickMu.Unlock()
				}
				collector.LogLatest()
			}
		}
	}()

	wg.Wait()
	elapsed := time.Since(startTime)
	collectorCancel()

	// Final tick to capture remaining data, per operation type.
	finalTicks := reg.Tick()
	total := metrics.MergeTicks(finalTicks, "total")

	// Per-operation summaries (only meaningful when a benchmark mixes ops).
	var opSummaries []metrics.OpSummary
	if len(finalTicks) > 1 {
		for _, ot := range finalTicks {
			opSummaries = append(opSummaries, metrics.OpSummary{
				Name:    ot.Name,
				Summary: metrics.BuildSummary(ot.Cumulative, elapsed),
			})
		}
	}

	// Build result
	tickMu.Lock()
	result := &metrics.Result{
		Config: &metrics.RunConfig{
			Pebble:    database.ResolvedConfig(),
			Benchmark: cfg.Benchmark,
		},
		Benchmark:   b.Name(),
		Duration:    elapsed,
		PebbleFinal: collector.Latest(),
		ReadAmpAvg:  collector.AvgReadAmp(),
		ReadAmpMax:  collector.MaxReadAmp(),
		Ticks:       tickRecords,
		OpSummaries: opSummaries,
		Summary:     metrics.BuildSummary(total.Cumulative, elapsed),
	}

	// Compute ops/sec min/max from tick records
	for _, tr := range result.Ticks {
		if result.Summary.OpsPerSecMin == 0 || tr.OpsPerSec < result.Summary.OpsPerSecMin {
			result.Summary.OpsPerSecMin = tr.OpsPerSec
		}
		if tr.OpsPerSec > result.Summary.OpsPerSecMax {
			result.Summary.OpsPerSecMax = tr.OpsPerSec
		}
	}
	tickMu.Unlock()
	return result, nil
}

func availableBenchmarks() []string {
	var names []string
	for name := range Registry {
		names = append(names, name)
	}
	return names
}

type opsCountKey struct{}

type opsCounter struct {
	count  *atomic.Int64
	max    int64
	cancel context.CancelFunc
}

// IncrementOps increments the ops counter and returns true if the benchmark should continue.
func IncrementOps(ctx context.Context) bool {
	v := ctx.Value(opsCountKey{})
	if v == nil {
		return true
	}
	counter := v.(*opsCounter)
	if counter.count.Add(1) >= counter.max {
		counter.cancel()
		return false
	}
	return true
}
