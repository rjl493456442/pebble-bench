package metrics

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
)

// PebbleSnapshot captures a point-in-time view of Pebble metrics.
type PebbleSnapshot struct {
	Timestamp         time.Time
	DiskUsage         uint64
	ReadAmplification int
	CompactionCount   int64
	CompactionDebt    uint64
	CompactionsActive int64
	MemTableSize      uint64
	MemTableCount     int64
	FlushStats        FlushStats      `json:"flush_stats"`
	WriteStallStats   WriteStallStats `json:"write_stall_stats"`
	BlockCacheHits    int64
	BlockCacheMisses  int64
	TableCacheHits    int64
	TableCacheMisses  int64
	FilterHits        int64
	FilterMisses      int64
	LevelSizes        [7]int64
	LevelFiles        [7]int64
}

// Collector periodically captures Pebble internal metrics.
type Collector struct {
	db                *pebble.DB
	interval          time.Duration
	flushTracker      *FlushTracker
	writeStallTracker *WriteStallTracker

	mu        sync.Mutex
	snapshots []PebbleSnapshot
}

// NewCollector creates a new metrics collector.
func NewCollector(db *pebble.DB, interval time.Duration, flushTracker *FlushTracker, writeStallTracker *WriteStallTracker) *Collector {
	return &Collector{
		db:                db,
		interval:          interval,
		flushTracker:      flushTracker,
		writeStallTracker: writeStallTracker,
	}
}

// Run starts the periodic collection. Call in a goroutine.
func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.capture()
		}
	}
}

func (c *Collector) capture() {
	m := c.db.Metrics()
	snap := PebbleSnapshot{
		Timestamp:         time.Now(),
		DiskUsage:         m.DiskSpaceUsage(),
		CompactionCount:   m.Compact.Count,
		CompactionDebt:    m.Compact.EstimatedDebt,
		CompactionsActive: m.Compact.NumInProgress,
		MemTableSize:      m.MemTable.Size,
		MemTableCount:     m.MemTable.Count,
		FlushStats:        c.flushTracker.Stats(),
		WriteStallStats:   c.writeStallTracker.Stats(),
		BlockCacheHits:    m.BlockCache.Hits,
		BlockCacheMisses:  m.BlockCache.Misses,
		TableCacheHits:    m.TableCache.Hits,
		TableCacheMisses:  m.TableCache.Misses,
		FilterHits:        m.Filter.Hits,
		FilterMisses:      m.Filter.Misses,
	}

	// Compute read amplification as total L0 sub-levels
	for i, l := range m.Levels {
		if i < 7 {
			snap.LevelSizes[i] = l.Size
			snap.LevelFiles[i] = l.NumFiles
		}
	}
	snap.ReadAmplification = int(m.ReadAmp())

	c.mu.Lock()
	c.snapshots = append(c.snapshots, snap)
	c.mu.Unlock()
}

// Latest returns the most recent snapshot, or zero value if none.
func (c *Collector) Latest() PebbleSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.snapshots) == 0 {
		return PebbleSnapshot{}
	}
	return c.snapshots[len(c.snapshots)-1]
}

// All returns all captured snapshots.
func (c *Collector) All() []PebbleSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]PebbleSnapshot, len(c.snapshots))
	copy(result, c.snapshots)
	return result
}

// LogLatest logs the most recent Pebble metrics snapshot.
func (c *Collector) LogLatest() {
	snap := c.Latest()
	if snap.Timestamp.IsZero() {
		return
	}
	log.Printf("Pebble: disk=%s read-amp=%d compactions=%d(active=%d) debt=%s memtable=%s(%d) flushes=%d(avg=%s) stalls=%d(total=%s) bcache=%d/%d tcache=%d/%d filter=%d/%d",
		formatSize(snap.DiskUsage),
		snap.ReadAmplification,
		snap.CompactionCount,
		snap.CompactionsActive,
		formatSize(snap.CompactionDebt),
		formatSize(snap.MemTableSize),
		snap.MemTableCount,
		snap.FlushStats.Count,
		snap.FlushStats.AvgTime().Round(time.Millisecond),
		snap.WriteStallStats.Count,
		snap.WriteStallStats.TotalTime.Round(time.Millisecond),
		snap.BlockCacheHits,
		snap.BlockCacheHits+snap.BlockCacheMisses,
		snap.TableCacheHits,
		snap.TableCacheHits+snap.TableCacheMisses,
		snap.FilterHits,
		snap.FilterHits+snap.FilterMisses,
	)
}

func formatSize(b uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return log2Fmt(float64(b)/float64(GB), "GB")
	case b >= MB:
		return log2Fmt(float64(b)/float64(MB), "MB")
	case b >= KB:
		return log2Fmt(float64(b)/float64(KB), "KB")
	default:
		return log2Fmt(float64(b), "B")
	}
}

func log2Fmt(val float64, suffix string) string {
	return fmtFloat(val) + suffix
}

func fmtFloat(val float64) string {
	if val >= 100 {
		return fmtInt(int64(val))
	}
	return fmtDec(val)
}

func fmtInt(v int64) string {
	return intToStr(v)
}

func fmtDec(v float64) string {
	return decToStr(v)
}

func intToStr(v int64) string {
	s := ""
	if v == 0 {
		return "0"
	}
	for v > 0 {
		s = string(rune('0'+v%10)) + s
		v /= 10
	}
	return s
}

func decToStr(v float64) string {
	whole := int64(v)
	frac := int64((v - float64(whole)) * 100)
	if frac < 0 {
		frac = -frac
	}
	w := intToStr(whole)
	f := intToStr(frac)
	if len(f) < 2 {
		f = "0" + f
	}
	return w + "." + f
}
