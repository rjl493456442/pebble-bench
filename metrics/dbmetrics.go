package metrics

// DBMetrics is a version-agnostic snapshot of the internal database metrics
// the collector cares about. The db package translates the Pebble-version
// specific metrics (v1 or v2) into this normalized form so the rest of the
// tool does not depend on a particular Pebble release.
type DBMetrics struct {
	DiskSpaceUsage    uint64
	ReadAmp           int
	WriteAmp          float64 // BytesWritten / WALBytesIn; not pebble's own, whose denominator is BytesIn
	BytesWritten      uint64  // bytes written to disk: every level's flushed and compacted bytes (incl. blob) plus the WAL
	BytesRead         uint64  // bytes read during compaction
	BytesIn           uint64  // pebble's own write-amp denominator, WAL.BytesWritten + ingested; see WALBytesWritten
	WALBytesIn        uint64  // logical bytes appended to the WAL (the real user ingest)
	WALBytesWritten   uint64  // pebble's WAL.BytesWritten: Levels[0].TableBytesIn + live WAL size, NOT physical bytes; intra-L0 input lands in it
	CompactionCount   int64
	CompactionDebt    uint64
	CompactionsActive int64
	MemTableSize      uint64
	MemTableCount     int64
	BlockCacheHits    int64
	BlockCacheMisses  int64
	TableCacheHits    int64
	TableCacheMisses  int64
	FilterHits        int64
	FilterMisses      int64
	LevelSizes        [7]int64
	LevelFiles        [7]int64

	// Per-level written-byte counters, and the base level they are read
	// against. Taken from each level's own counters rather than from
	// compaction inputs: a compaction drops overwritten keys and tombstones in
	// the merge, so its inputs overstate what actually lands on disk. Summed
	// this way the stages account for every byte written.
	Levels [7]LevelStat

	// BaseLevel is the shallowest level below L0 holding data, the one L0
	// drains into. Negative when nothing has been compacted out of L0 yet.
	BaseLevel int

	// L0 broken down by sublevel, deepest last. Files inside one sublevel never
	// overlap, so a sublevel holding many files that span most of the key range
	// is a tiled layer, while one holding few files over a narrow span is a
	// column. A workload writing keys in order produces the former, and can
	// leave L0 holding thousands of files at a depth of one or two.
	L0Sublevels []SublevelStat
	L0Shape     L0Shape
}

// LevelStat holds one level's cumulative byte counters.
//
// BytesMoved deserves attention on any workload writing keys in order. A move
// compaction relinks a table into the level below instead of rewriting it,
// which happens whenever the table's key range overlaps nothing down there, so
// the bytes counted here are bytes the LSM carried for free. Where they are
// large, the settings that decide how wide a table's key range is — the target
// file sizes above all — matter more than anything else in the config.
type LevelStat struct {
	BytesFlushed   uint64 `json:"bytes_flushed"`
	BytesCompacted uint64 `json:"bytes_compacted"`
	BytesRead      uint64 `json:"bytes_read"`
	BytesMoved     uint64 `json:"bytes_moved"`
}

// SublevelStat describes one L0 sublevel.
type SublevelStat struct {
	Sublevel int     `json:"sublevel"`
	Files    int64   `json:"files"`
	Size     int64   `json:"size"`
	Span     float64 `json:"span"` // share of L0's key range this sublevel covers
}

// L0Shape measures how well the files of adjacent sublevels line up, which is
// what decides how wide an L0->Lbase compaction grows as it stacks sublevels.
//
// A candidate that adds a sublevel must take every older file its key range
// touches. If files sit on a shared grid, that is exactly one file per
// sublevel and the candidate stays a column; if edges are staggered, it is
// two or more, the range widens, and the next sublevel down widens it again.
// The base level bytes rewritten per byte of L0 carried down scale with that
// width, so StepFanout is the geometric factor behind the drain's write
// amplification and the collisions between concurrent drains.
type L0Shape struct {
	Files    int64 `json:"files"`    // files in L0
	Measured int64 `json:"measured"` // files with a sublevel below them, ie not in sublevel 0

	// StepFanout is the mean number of files a file overlaps in the sublevel
	// directly below it. 1.0 means the grid is shared; each further 1.0 is
	// one more file the drain must take per sublevel it descends.
	StepFanout    float64 `json:"step_fanout"`
	StepFanoutMax int64   `json:"step_fanout_max"`

	// AlignedStarts is the share of measured files whose first key is also
	// the first key of a file they overlap in the sublevel below: a shared
	// edge. Near 1.0 when flushes cut only at the shared barriers, and low
	// when they also cut at the base level's file edges, which move.
	AlignedStarts float64 `json:"aligned_starts"`

	// StepWidening is what the drain actually pays for: the mean factor by
	// which a file's key range grows once the files it overlaps in the
	// sublevel below are taken with it. 1.0 means a candidate stays a column
	// as it stacks; 1.5 means each sublevel it descends widens it by half.
	// It differs from StepFanout on nested grids: a 16MB file over two 8MB
	// files has a fanout of 2 and a widening of 1.0, and it is the widening
	// that sets how much of the base level the drain rewrites.
	StepWidening    float64 `json:"step_widening"`
	StepWideningMax float64 `json:"step_widening_max"`
}

// MetricsSource is implemented by anything that can report a normalized
// snapshot of the database metrics (i.e. an opened database).
type MetricsSource interface {
	Metrics() *DBMetrics
}
