package metrics

import (
	"sync"
	"time"
)

// SyncOp identifies one of the three durability-related file operations Pebble
// issues through its VFS layer. Each maps to a distinct syscall on Linux.
type SyncOp int

const (
	// OpSync is vfs.File.Sync, backed by fsync(2) (fcntl(F_FULLFSYNC) on macOS).
	OpSync SyncOp = iota
	// OpSyncData is vfs.File.SyncData, backed by fdatasync(2) on Linux and by
	// fsync where fdatasync is unavailable.
	OpSyncData
	// OpSyncTo is vfs.File.SyncTo, backed by sync_file_range(2) on Linux and a
	// no-op elsewhere.
	OpSyncTo

	numSyncOps
)

// SyncStats is a snapshot of all sync-operation statistics, labelled by the
// underlying syscall for clarity in reports.
type SyncStats struct {
	Sync     IOStat `json:"fsync"`
	SyncData IOStat `json:"fdatasync"`
	SyncTo   IOStat `json:"sync_file_range"`
}

// SyncTracker records the count and timing of fsync/fdatasync/sync_file_range
// calls observed at the VFS boundary. It is safe for concurrent use. The
// per-call overhead is a single time measurement plus a short critical section,
// which is negligible relative to the syscall being measured.
type SyncTracker struct {
	mu    sync.Mutex
	stats [numSyncOps]IOStat
}

// NewSyncTracker creates a new SyncTracker.
func NewSyncTracker() *SyncTracker {
	return &SyncTracker{}
}

// Record records a single sync operation of the given kind and duration.
func (t *SyncTracker) Record(op SyncOp, d time.Duration) {
	if op < 0 || op >= numSyncOps {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	s := &t.stats[op]
	s.Count++
	s.TotalTime += d
	if s.Count == 1 || d < s.MinTime {
		s.MinTime = d
	}
	if d > s.MaxTime {
		s.MaxTime = d
	}
}

// Stats returns a snapshot of the current statistics.
func (t *SyncTracker) Stats() SyncStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return SyncStats{
		Sync:     t.stats[OpSync],
		SyncData: t.stats[OpSyncData],
		SyncTo:   t.stats[OpSyncTo],
	}
}
