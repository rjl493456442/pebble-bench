package metrics

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

const (
	sigFigs    = 1
	minLatency = 1 * time.Microsecond
	maxLatency = 10 * time.Second
)

// NamedHistogram is a thread-safe latency histogram with a name.
type NamedHistogram struct {
	name       string
	mu         sync.Mutex
	hist       *hdrhistogram.Histogram
	cumulative *hdrhistogram.Histogram
	startTime  time.Time
}

// NewNamedHistogram creates a new named histogram.
func NewNamedHistogram(name string) *NamedHistogram {
	return &NamedHistogram{
		name:       name,
		hist:       hdrhistogram.New(minLatency.Nanoseconds(), maxLatency.Nanoseconds(), sigFigs),
		cumulative: hdrhistogram.New(minLatency.Nanoseconds(), maxLatency.Nanoseconds(), sigFigs),
		startTime:  time.Now(),
	}
}

// Record records a latency sample.
func (h *NamedHistogram) Record(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = h.hist.RecordValue(d.Nanoseconds())
}

// HistogramTick contains a snapshot for a tick interval.
type HistogramTick struct {
	Name       string
	Elapsed    time.Duration
	Cumulative *hdrhistogram.Histogram
	Hist       *hdrhistogram.Histogram // interval histogram
}

// Tick snapshots the current interval histogram, merges it into the
// cumulative histogram, and resets the interval. Returns the tick data.
func (h *NamedHistogram) Tick() HistogramTick {
	h.mu.Lock()
	defer h.mu.Unlock()

	snap := hdrhistogram.Import(h.hist.Export())
	h.hist.Reset()
	h.cumulative.Merge(snap)

	return HistogramTick{
		Name:       h.name,
		Elapsed:    time.Since(h.startTime),
		Cumulative: hdrhistogram.Import(h.cumulative.Export()),
		Hist:       snap,
	}
}

// PrintTick writes a formatted tick line to the writer.
func PrintTick(w io.Writer, tick HistogramTick) {
	h := tick.Hist
	if h.TotalCount() == 0 {
		return
	}
	opsPerSec := float64(h.TotalCount()) // per tick interval (1s)

	fmt.Fprintf(w, "%7s %12s %10.0f ops/sec %8.2fms p50 %8.2fms p95 %8.2fms p99 %8.2fms p99.9 [cum: %d ops, %.0f ops/sec]\n",
		tick.Elapsed.Truncate(time.Second),
		tick.Name,
		opsPerSec,
		nsToMs(h.ValueAtPercentile(50)),
		nsToMs(h.ValueAtPercentile(95)),
		nsToMs(h.ValueAtPercentile(99)),
		nsToMs(h.ValueAtPercentile(99.9)),
		tick.Cumulative.TotalCount(),
		float64(tick.Cumulative.TotalCount())/tick.Elapsed.Seconds(),
	)
}

func nsToMs(ns int64) float64 {
	return float64(ns) / float64(time.Millisecond)
}
