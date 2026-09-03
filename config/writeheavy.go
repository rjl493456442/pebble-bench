package config

const (
	WriteHeavyLBaseMaxBytes         int64 = 4 << 30
	WriteHeavyL0CompactionThreshold       = 4
	WriteHeavyL0StopWritesThreshold       = 24
	WriteHeavyTargetFileSizeL0      int64 = 16 << 20
	WriteHeavyTargetFileSizeLbase   int64 = 32 << 20
	WriteHeavyTargetFileSizeLbase1  int64 = 64 << 20
	WriteHeavyTargetFileSizeDeep    int64 = 128 << 20
	WriteHeavyMemTableCount               = 4
	WriteHeavyCacheDivisor                = 8
	WriteHeavyMinCacheMB                  = 128
	WriteHeavyBloomFilterBits             = 10
)

// ApplyWriteHeavy re-tunes the config for a write-dominated phase.
func (c *BenchConfig) ApplyWriteHeavy() {
	blockCacheMB := c.CacheMB / WriteHeavyCacheDivisor
	if blockCacheMB < WriteHeavyMinCacheMB {
		blockCacheMB = WriteHeavyMinCacheMB
	}
	memBudgetMB := max(c.CacheMB-blockCacheMB, 0)
	c.CacheMB = blockCacheMB

	c.MemTableCount = intPtr(WriteHeavyMemTableCount)
	c.MemTableStopWritesThreshold = intPtr(WriteHeavyMemTableCount * 2)
	if memBudgetMB > 0 {
		// Budget against the stop-writes threshold, as that many memtables can
		// exist at once including the frozen ones awaiting a flush.
		c.MemTableSize = intPtr(memBudgetMB * 1024 * 1024 / *c.MemTableStopWritesThreshold)
	}

	// Let L0 accumulate, and raise the ceiling along with it.
	c.L0CompactionThreshold = intPtr(WriteHeavyL0CompactionThreshold)
	c.L0StopWritesThreshold = intPtr(WriteHeavyL0StopWritesThreshold)

	// Land L0 in a deep base level so a byte crosses as few levels as it can on
	// its way to the bottom.
	lbase := WriteHeavyLBaseMaxBytes
	c.LBaseMaxBytes = &lbase

	// Pebble derives FlushSplitBytes from the L0 target and the target also caps
	// the tables a flush emits, so leaving both small shatters one memtable flush
	// into a shower of little tables.
	for len(c.Levels) < 7 {
		c.Levels = append(c.Levels, LevelConfig{})
	}
	// The targets are indexed relative to the base level, not by absolute level
	// number: [0] is L0, [1] is the base level, [2] is base+1, and so on, so this
	// ladder needs no adjustment when LBaseMaxBytes moves the base level.
	c.Levels[0].TargetFileSize = WriteHeavyTargetFileSizeL0
	c.Levels[1].TargetFileSize = WriteHeavyTargetFileSizeLbase
	c.Levels[2].TargetFileSize = WriteHeavyTargetFileSizeLbase1
	for i := 3; i < len(c.Levels); i++ {
		c.Levels[i].TargetFileSize = WriteHeavyTargetFileSizeDeep
	}
	split := c.Levels[0].TargetFileSize
	c.FlushSplitBytes = &split

	c.BloomFilterBits = intPtr(WriteHeavyBloomFilterBits)
	for i := range c.Levels {
		c.Levels[i].NoFilter = i == len(c.Levels)-1
		c.Levels[i].BloomFilterBits = nil
	}
}
