package metrics

import (
	"math"
	"sync/atomic"
	"time"
)

// ReadOp identifies one of the read operations Pebble issues through its VFS
// layer. Each maps to a distinct syscall.
type ReadOp int

const (
	// OpRead is vfs.File.Read, backed by read(2) (sequential reads, e.g. WAL
	// replay and manifest loading).
	OpRead ReadOp = iota
	// OpReadAt is vfs.File.ReadAt, backed by pread(2). This is the dominant path
	// for sstable block reads that miss the block cache, i.e. real disk reads
	// during point lookups, scans, and compactions.
	OpReadAt

	numReadOps
)

// ReadStats is a snapshot of all read-operation statistics, labelled by the
// underlying syscall for clarity in reports.
type ReadStats struct {
	Read   IOStat `json:"read"`
	ReadAt IOStat `json:"pread"`
}

// readAccum is the lock-free accumulator for a single read operation type.
// Reads are a hot, highly concurrent path (one call per block-cache miss), so
// the tracker avoids a mutex to keep from adding contention that would distort
// the latencies being measured.
type readAccum struct {
	count      atomic.Int64
	totalNanos atomic.Int64
	minNanos   atomic.Int64
	maxNanos   atomic.Int64
}

func (a *readAccum) record(d time.Duration) {
	n := int64(d)
	a.count.Add(1)
	a.totalNanos.Add(n)
	for {
		cur := a.maxNanos.Load()
		if n <= cur || a.maxNanos.CompareAndSwap(cur, n) {
			break
		}
	}
	for {
		cur := a.minNanos.Load()
		if n >= cur || a.minNanos.CompareAndSwap(cur, n) {
			break
		}
	}
}

func (a *readAccum) snapshot() IOStat {
	c := a.count.Load()
	s := IOStat{
		Count:     c,
		TotalTime: time.Duration(a.totalNanos.Load()),
		MaxTime:   time.Duration(a.maxNanos.Load()),
	}
	if c > 0 {
		s.MinTime = time.Duration(a.minNanos.Load())
	}
	return s
}

// ReadTracker records the count and timing of read(2)/pread(2) calls observed
// at the VFS boundary. It is lock-free and safe for concurrent use.
type ReadTracker struct {
	accum [numReadOps]readAccum
}

// NewReadTracker creates a new ReadTracker.
func NewReadTracker() *ReadTracker {
	t := &ReadTracker{}
	for i := range t.accum {
		t.accum[i].minNanos.Store(math.MaxInt64)
	}
	return t
}

// Record records a single read operation of the given kind and duration.
func (t *ReadTracker) Record(op ReadOp, d time.Duration) {
	if op < 0 || op >= numReadOps {
		return
	}
	t.accum[op].record(d)
}

// Stats returns a snapshot of the current statistics.
func (t *ReadTracker) Stats() ReadStats {
	return ReadStats{
		Read:   t.accum[OpRead].snapshot(),
		ReadAt: t.accum[OpReadAt].snapshot(),
	}
}
