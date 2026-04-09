package metrics

import (
	"sync"
	"time"
)

// FlushStats summarizes flush durations observed during a benchmark.
type FlushStats struct {
	Count      int64         `json:"count"`
	TotalTime  time.Duration `json:"total_time"`
	MinTime    time.Duration `json:"min_time"`
	MaxTime    time.Duration `json:"max_time"`
	TotalBytes uint64        `json:"total_bytes"`
}

// AvgTime returns the average flush duration, or zero if no flushes occurred.
func (f FlushStats) AvgTime() time.Duration {
	if f.Count == 0 {
		return 0
	}
	return f.TotalTime / time.Duration(f.Count)
}

// FlushTracker records per-flush timing from Pebble's EventListener.
type FlushTracker struct {
	mu    sync.Mutex
	stats FlushStats
}

// NewFlushTracker creates a new FlushTracker.
func NewFlushTracker() *FlushTracker {
	return &FlushTracker{}
}

// Record records a single flush event.
func (t *FlushTracker) Record(duration time.Duration, inputBytes uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stats.Count++
	t.stats.TotalTime += duration
	t.stats.TotalBytes += inputBytes
	if t.stats.Count == 1 || duration < t.stats.MinTime {
		t.stats.MinTime = duration
	}
	if duration > t.stats.MaxTime {
		t.stats.MaxTime = duration
	}
}

// Stats returns a snapshot of the current flush statistics.
func (t *FlushTracker) Stats() FlushStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}
