package metrics

import (
	"sync"
	"time"
)

// WriteStallStats summarizes write stall events observed during a benchmark.
type WriteStallStats struct {
	Count     int64         `json:"count"`
	TotalTime time.Duration `json:"total_time"`
	MaxTime   time.Duration `json:"max_time"`
}

// AvgTime returns the average stall duration, or zero if no stalls occurred.
func (s WriteStallStats) AvgTime() time.Duration {
	if s.Count == 0 {
		return 0
	}
	return s.TotalTime / time.Duration(s.Count)
}

// WriteStallTracker records write stall events from Pebble's EventListener.
type WriteStallTracker struct {
	mu        sync.Mutex
	stats     WriteStallStats
	stallTime time.Time // when the current stall began
}

// NewWriteStallTracker creates a new WriteStallTracker.
func NewWriteStallTracker() *WriteStallTracker {
	return &WriteStallTracker{}
}

// Begin marks the start of a write stall.
func (t *WriteStallTracker) Begin() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stallTime = time.Now()
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
}

// Stats returns a snapshot of the current write stall statistics.
func (t *WriteStallTracker) Stats() WriteStallStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}
