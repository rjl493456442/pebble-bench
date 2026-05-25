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
	lastTick   time.Time
}

// NewNamedHistogram creates a new named histogram.
func NewNamedHistogram(name string) *NamedHistogram {
	now := time.Now()
	return &NamedHistogram{
		name:       name,
		hist:       hdrhistogram.New(minLatency.Nanoseconds(), maxLatency.Nanoseconds(), sigFigs),
		cumulative: hdrhistogram.New(minLatency.Nanoseconds(), maxLatency.Nanoseconds(), sigFigs),
		startTime:  now,
		lastTick:   now,
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
	Elapsed    time.Duration // total time since the histogram was created
	Interval   time.Duration // time since the previous tick
	Cumulative *hdrhistogram.Histogram
	Hist       *hdrhistogram.Histogram // interval histogram
}

// IntervalOpsPerSec returns the operation rate over the tick's interval window.
// It divides the interval op count by the actual elapsed interval, so the value
// is comparable to the cumulative rate regardless of the tick period.
func (t HistogramTick) IntervalOpsPerSec() float64 {
	if t.Interval <= 0 {
		return float64(t.Hist.TotalCount())
	}
	return float64(t.Hist.TotalCount()) / t.Interval.Seconds()
}

// CumulativeOpsPerSec returns the operation rate over the whole run so far.
func (t HistogramTick) CumulativeOpsPerSec() float64 {
	if t.Elapsed <= 0 {
		return float64(t.Cumulative.TotalCount())
	}
	return float64(t.Cumulative.TotalCount()) / t.Elapsed.Seconds()
}

// Tick snapshots the current interval histogram, merges it into the
// cumulative histogram, and resets the interval. Returns the tick data.
func (h *NamedHistogram) Tick() HistogramTick {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	snap := hdrhistogram.Import(h.hist.Export())
	h.hist.Reset()
	h.cumulative.Merge(snap)

	interval := now.Sub(h.lastTick)
	h.lastTick = now

	return HistogramTick{
		Name:       h.name,
		Elapsed:    now.Sub(h.startTime),
		Interval:   interval,
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

	fmt.Fprintf(w, "%7s %12s %10.0f ops/sec %8.2fms p50 %8.2fms p95 %8.2fms p99 %8.2fms p99.9 [cum: %d ops, %.0f ops/sec]\n",
		tick.Elapsed.Truncate(time.Second),
		tick.Name,
		tick.IntervalOpsPerSec(),
		nsToMs(h.ValueAtPercentile(50)),
		nsToMs(h.ValueAtPercentile(95)),
		nsToMs(h.ValueAtPercentile(99)),
		nsToMs(h.ValueAtPercentile(99.9)),
		tick.Cumulative.TotalCount(),
		tick.CumulativeOpsPerSec(),
	)
}

func nsToMs(ns int64) float64 {
	return float64(ns) / float64(time.Millisecond)
}
