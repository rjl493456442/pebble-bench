package metrics

import (
	"context"
	"log"
	"sync"
	"time"
)

// PebbleSnapshot captures a point-in-time view of Pebble metrics.
type PebbleSnapshot struct {
	Timestamp         time.Time
	DiskUsage         uint64
	ReadAmplification int
	WriteAmp          float64
	BytesWritten      uint64
	BytesRead         uint64
	BytesIn           uint64
	CompactionCount   int64
	CompactionDebt    uint64
	CompactionsActive int64
	MemTableSize      uint64
	MemTableCount     int64
	FlushStats        FlushStats      `json:"flush_stats"`
	WriteStallStats   WriteStallStats `json:"write_stall_stats"`
	SyncStats         SyncStats       `json:"sync_stats"`
	ReadStats         ReadStats       `json:"read_stats"`
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
	src               MetricsSource
	interval          time.Duration
	flushTracker      *FlushTracker
	writeStallTracker *WriteStallTracker
	syncTracker       *SyncTracker
	readTracker       *ReadTracker

	mu        sync.Mutex
	snapshots []PebbleSnapshot
}

// NewCollector creates a new metrics collector.
func NewCollector(src MetricsSource, interval time.Duration, flushTracker *FlushTracker, writeStallTracker *WriteStallTracker, syncTracker *SyncTracker, readTracker *ReadTracker) *Collector {
	return &Collector{
		src:               src,
		interval:          interval,
		flushTracker:      flushTracker,
		writeStallTracker: writeStallTracker,
		syncTracker:       syncTracker,
		readTracker:       readTracker,
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
	m := c.src.Metrics()
	snap := PebbleSnapshot{
		Timestamp:         time.Now(),
		DiskUsage:         m.DiskSpaceUsage,
		ReadAmplification: m.ReadAmp,
		WriteAmp:          m.WriteAmp,
		BytesWritten:      m.BytesWritten,
		BytesRead:         m.BytesRead,
		BytesIn:           m.BytesIn,
		CompactionCount:   m.CompactionCount,
		CompactionDebt:    m.CompactionDebt,
		CompactionsActive: m.CompactionsActive,
		MemTableSize:      m.MemTableSize,
		MemTableCount:     m.MemTableCount,
		FlushStats:        c.flushTracker.Stats(),
		WriteStallStats:   c.writeStallTracker.Stats(),
		SyncStats:         c.syncTracker.Stats(),
		ReadStats:         c.readTracker.Stats(),
		BlockCacheHits:    m.BlockCacheHits,
		BlockCacheMisses:  m.BlockCacheMisses,
		TableCacheHits:    m.TableCacheHits,
		TableCacheMisses:  m.TableCacheMisses,
		FilterHits:        m.FilterHits,
		FilterMisses:      m.FilterMisses,
		LevelSizes:        m.LevelSizes,
		LevelFiles:        m.LevelFiles,
	}

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

// AvgReadAmp returns the mean read amplification across all captured snapshots.
// Unlike the final snapshot, this reflects the read amplification sustained over
// the whole run (matching how Pebble's own ycsb benchmark reports r-amp).
func (c *Collector) AvgReadAmp() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.snapshots) == 0 {
		return 0
	}
	var sum int
	for _, s := range c.snapshots {
		sum += s.ReadAmplification
	}
	return float64(sum) / float64(len(c.snapshots))
}

// MaxReadAmp returns the peak read amplification observed during the run.
func (c *Collector) MaxReadAmp() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	var maxAmp int
	for _, s := range c.snapshots {
		if s.ReadAmplification > maxAmp {
			maxAmp = s.ReadAmplification
		}
	}
	return maxAmp
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
	log.Printf("Pebble: disk=%s read-amp=%d write-amp=%.2f compactions=%d(active=%d) debt=%s memtable=%s(%d) flushes=%d(avg=%s) stalls=%d(total=%s) bcache=%d/%d tcache=%d/%d filter=%d/%d",
		FormatSize(snap.DiskUsage),
		snap.ReadAmplification,
		snap.WriteAmp,
		snap.CompactionCount,
		snap.CompactionsActive,
		FormatSize(snap.CompactionDebt),
		FormatSize(snap.MemTableSize),
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

func FormatSize(b uint64) string {
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
