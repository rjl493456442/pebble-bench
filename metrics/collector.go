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
	CompactionStats   CompactionStats `json:"compaction_stats"`
	BlockCacheHits    int64
	BlockCacheMisses  int64
	TableCacheHits    int64
	TableCacheMisses  int64
	FilterHits        int64
	FilterMisses      int64
	LevelSizes        [7]int64
	LevelFiles        [7]int64
	L0Sublevels       []SublevelStat `json:"l0_sublevels,omitempty"`

	// Everything the stage breakdown is computed from, carried across so a
	// report can decompose the write amplification it prints instead of only
	// quoting the ratio.
	Levels          [7]LevelStat `json:"levels"`
	BaseLevel       int          `json:"base_level"`
	WALBytesIn      uint64       `json:"wal_bytes_in"`
	WALBytesWritten uint64       `json:"wal_bytes_written"`
}

// StageBreakdown decomposes the snapshot's write volume by the step that wrote
// it. See BuildStageBreakdown for what the rows mean and why they are taken
// from the level counters.
func (s PebbleSnapshot) StageBreakdown() StageBreakdown {
	return BuildStageBreakdown(&DBMetrics{
		BytesWritten:    s.BytesWritten,
		WALBytesIn:      s.WALBytesIn,
		WALBytesWritten: s.WALBytesWritten,
		Levels:          s.Levels,
		BaseLevel:       s.BaseLevel,
	})
}

// Collector periodically captures Pebble internal metrics.
type Collector struct {
	src               MetricsSource
	interval          time.Duration
	flushTracker      *FlushTracker
	writeStallTracker *WriteStallTracker
	syncTracker       *SyncTracker
	readTracker       *ReadTracker
	compactionTracker *CompactionTracker

	mu        sync.Mutex
	snapshots []PebbleSnapshot
}

// NewCollector creates a new metrics collector.
func NewCollector(src MetricsSource, interval time.Duration, flushTracker *FlushTracker, writeStallTracker *WriteStallTracker, syncTracker *SyncTracker, readTracker *ReadTracker, compactionTracker *CompactionTracker) *Collector {
	return &Collector{
		src:               src,
		interval:          interval,
		flushTracker:      flushTracker,
		writeStallTracker: writeStallTracker,
		syncTracker:       syncTracker,
		readTracker:       readTracker,
		compactionTracker: compactionTracker,
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
	// Push the fresh per-level size snapshot into the compaction tracker so
	// the next batch of CompactionEnd events can compute fan-in / destination
	// pct against an up-to-date total. Up to one tick of staleness is fine
	// since per-level totals move slowly under steady-state writes.
	if c.compactionTracker != nil {
		c.compactionTracker.SetLevelBytes(m.LevelSizes)
	}
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
		CompactionStats:   c.compactionStatsOrZero(),
		BlockCacheHits:    m.BlockCacheHits,
		BlockCacheMisses:  m.BlockCacheMisses,
		TableCacheHits:    m.TableCacheHits,
		TableCacheMisses:  m.TableCacheMisses,
		FilterHits:        m.FilterHits,
		FilterMisses:      m.FilterMisses,
		LevelSizes:        m.LevelSizes,
		LevelFiles:        m.LevelFiles,
		L0Sublevels:       m.L0Sublevels,
		Levels:            m.Levels,
		BaseLevel:         m.BaseLevel,
		WALBytesIn:        m.WALBytesIn,
		WALBytesWritten:   m.WALBytesWritten,
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

// Snapshot captures the store's metrics now and returns them, rather than
// handing back the last tick, so a caller marking a boundary gets the state at
// the boundary and not up to one interval before it.
func (c *Collector) Snapshot() PebbleSnapshot {
	c.capture()
	return c.Latest()
}

// Interval returns the collection period.
func (c *Collector) Interval() time.Duration { return c.interval }

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

// compactionStatsOrZero returns the current compaction-tracker snapshot, or a
// zero value if no tracker was wired in (e.g. the init path).
func (c *Collector) compactionStatsOrZero() CompactionStats {
	if c.compactionTracker == nil {
		return CompactionStats{}
	}
	return c.compactionTracker.Stats()
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
