package metrics

// DBMetrics is a version-agnostic snapshot of the internal database metrics
// the collector cares about. The db package translates the Pebble-version
// specific metrics (v1 or v2) into this normalized form so the rest of the
// tool does not depend on a particular Pebble release.
type DBMetrics struct {
	DiskSpaceUsage    uint64
	ReadAmplification int
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
}

// MetricsSource is implemented by anything that can report a normalized
// snapshot of the database metrics (i.e. an opened database).
type MetricsSource interface {
	Metrics() *DBMetrics
}
