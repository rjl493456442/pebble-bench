package metrics

import (
	"sort"
	"sync"
	"time"
)

// WriteStallStats summarizes write stall events observed during a benchmark.
type WriteStallStats struct {
	Count     int64         `json:"count"`
	TotalTime time.Duration `json:"total_time"`
	MaxTime   time.Duration `json:"max_time"`

	// ByReason splits the stalls by the reason pebble gave for each. The
	// reason is the string from WriteStallBeginInfo, which today is one of
	// "memtable count limit reached" and "L0 file count limit exceeded", and
	// the split matters more than the totals: the first says the flush cannot
	// keep up with ingest, the second that the L0 drain cannot keep up with
	// the flush, and a configuration change that trades one for the other can
	// leave the total unchanged while moving the bottleneck entirely.
	ByReason map[string]StallReasonStats `json:"by_reason,omitempty"`
}

// StallReasonStats is the share of the stalls attributed to one reason.
type StallReasonStats struct {
	Count     int64         `json:"count"`
	TotalTime time.Duration `json:"total_time"`
}

// AvgTime returns the average stall duration, or zero if no stalls occurred.
func (s WriteStallStats) AvgTime() time.Duration {
	if s.Count == 0 {
		return 0
	}
	return s.TotalTime / time.Duration(s.Count)
}

// Reasons returns the stall reasons seen, most time-consuming first, so that
// a report lists them in a stable and useful order.
func (s WriteStallStats) Reasons() []string {
	reasons := make([]string, 0, len(s.ByReason))
	for r := range s.ByReason {
		reasons = append(reasons, r)
	}
	sort.Slice(reasons, func(i, j int) bool {
		a, b := s.ByReason[reasons[i]], s.ByReason[reasons[j]]
		if a.TotalTime != b.TotalTime {
			return a.TotalTime > b.TotalTime
		}
		return reasons[i] < reasons[j]
	})
	return reasons
}

// WriteStallTracker records write stall events from Pebble's EventListener.
type WriteStallTracker struct {
	mu        sync.Mutex
	stats     WriteStallStats
	stallTime time.Time // when the current stall began
	reason    string    // why, as pebble reported it
}

// NewWriteStallTracker creates a new WriteStallTracker.
func NewWriteStallTracker() *WriteStallTracker {
	return &WriteStallTracker{}
}

// Begin marks the start of a write stall. reason is pebble's own description
// of what stalled the write, kept so End can attribute the duration to it.
func (t *WriteStallTracker) Begin(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stallTime = time.Now()
	t.reason = reason
}

// End marks the end of a write stall and records its duration.
func (t *WriteStallTracker) End() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stallTime.IsZero() {
		return
	}
	duration := time.Since(t.stallTime)
	t.stallTime = time.Time{}

	t.stats.Count++
	t.stats.TotalTime += duration
	if duration > t.stats.MaxTime {
		t.stats.MaxTime = duration
	}
	if t.stats.ByReason == nil {
		t.stats.ByReason = make(map[string]StallReasonStats)
	}
	r := t.stats.ByReason[t.reason]
	r.Count++
	r.TotalTime += duration
	t.stats.ByReason[t.reason] = r
}

// Stats returns a snapshot of the current write stall statistics. The reason
// map is copied so the caller cannot see later updates through it.
func (t *WriteStallTracker) Stats() WriteStallStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.stats
	if t.stats.ByReason != nil {
		out.ByReason = make(map[string]StallReasonStats, len(t.stats.ByReason))
		for k, v := range t.stats.ByReason {
			out.ByReason[k] = v
		}
	}
	return out
}
