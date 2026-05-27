package metrics

import "time"

// IOStat summarizes the count and timing of a single instrumented I/O operation
// (e.g. one syscall family observed at the VFS boundary). It is a plain,
// copyable snapshot; trackers accumulate internally and produce these on demand.
type IOStat struct {
	Count     int64         `json:"count"`
	TotalTime time.Duration `json:"total_time"`
	MinTime   time.Duration `json:"min_time"`
	MaxTime   time.Duration `json:"max_time"`
}

// AvgTime returns the average duration of the operation, or zero if it never
// occurred.
func (s IOStat) AvgTime() time.Duration {
	if s.Count == 0 {
		return 0
	}
	return s.TotalTime / time.Duration(s.Count)
}
