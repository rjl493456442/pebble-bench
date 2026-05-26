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
	return newNamedHistogramAt(name, time.Now())
}

// newNamedHistogramAt creates a named histogram whose elapsed time is measured
// from the given start, so several histograms in a registry share one origin.
func newNamedHistogramAt(name string, start time.Time) *NamedHistogram {
	return &NamedHistogram{
		name:       name,
		hist:       hdrhistogram.New(minLatency.Nanoseconds(), maxLatency.Nanoseconds(), sigFigs),
		cumulative: hdrhistogram.New(minLatency.Nanoseconds(), maxLatency.Nanoseconds(), sigFigs),
		startTime:  start,
		lastTick:   start,
	}
}

// HistogramRegistry holds a set of named latency histograms, one per operation
// type (e.g. "read" and "write" for the mixed benchmark). Histograms are created
// lazily on first use and all share a common start time.
type HistogramRegistry struct {
	start  time.Time
	mu     sync.Mutex
	names  []string
	byName map[string]*NamedHistogram
}

// NewHistogramRegistry creates an empty registry.
func NewHistogramRegistry() *HistogramRegistry {
	return &HistogramRegistry{
		start:  time.Now(),
		byName: make(map[string]*NamedHistogram),
	}
}

// Get returns the histogram for the named operation, creating it on first use.
// It is safe to call concurrently from multiple worker goroutines.
func (r *HistogramRegistry) Get(name string) *NamedHistogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.byName[name]
	if !ok {
		h = newNamedHistogramAt(name, r.start)
		r.byName[name] = h
		r.names = append(r.names, name)
	}
	return h
}

// Tick snapshots every registered histogram (in registration order) and returns
// their ticks.
func (r *HistogramRegistry) Tick() []HistogramTick {
	r.mu.Lock()
	defer r.mu.Unlock()
	ticks := make([]HistogramTick, 0, len(r.names))
	for _, n := range r.names {
		ticks = append(ticks, r.byName[n].Tick())
	}
	return ticks
}

// MergeTicks merges several per-operation ticks into a single aggregate tick
// (used to produce the combined "total" line and time series).
func MergeTicks(ticks []HistogramTick, name string) HistogramTick {
	merged := HistogramTick{
		Name:       name,
		Hist:       hdrhistogram.New(minLatency.Nanoseconds(), maxLatency.Nanoseconds(), sigFigs),
		Cumulative: hdrhistogram.New(minLatency.Nanoseconds(), maxLatency.Nanoseconds(), sigFigs),
	}
	for i, t := range ticks {
		if i == 0 {
			merged.Elapsed = t.Elapsed
			merged.Interval = t.Interval
		}
		merged.Hist.Merge(t.Hist)
		merged.Cumulative.Merge(t.Cumulative)
	}
	return merged
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

// BuildSummary derives a Summary from a cumulative histogram over the given
// elapsed run time. OpsPerSecMin/Max are left zero; the caller fills those for
// the aggregate summary from the per-interval tick records.
func BuildSummary(h *hdrhistogram.Histogram, elapsed time.Duration) Summary {
	us := int64(time.Microsecond)
	s := Summary{
		TotalOps: h.TotalCount(),
		AvgUs:    int64(h.Mean()) / us,
		MinUs:    h.Min() / us,
		P50Us:    h.ValueAtPercentile(50) / us,
		P95Us:    h.ValueAtPercentile(95) / us,
		P99Us:    h.ValueAtPercentile(99) / us,
		P999Us:   h.ValueAtPercentile(99.9) / us,
		MaxUs:    h.Max() / us,
		StdDevUs: int64(h.StdDev()) / us,
	}
	if elapsed > 0 {
		s.OpsPerSec = float64(h.TotalCount()) / elapsed.Seconds()
	}
	return s
}
