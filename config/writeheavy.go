package config

// Sizing of the write-heavy profile.
//
// The profile targets a bounded burst of writes that are dominated by keys never
// seen before and read almost not at all, which is what the state download of a
// snap sync looks like. Its aim is to cut write amplification rather than to
// spread the same work out: a key with no prior version reclaims nothing when it
// is compacted, so every compaction output is the full sum of its inputs and the
// only product is a shape that reads better. Merging later, and fewer times,
// therefore removes rewrites outright.
//
// Two of the knobs below are coupled and have to move together. An L0 -> Lbase
// compaction rewrites the part of Lbase the batch overlaps, so a deep base level
// drained in small batches is rewritten once per batch, which costs more than
// the levels the deeper base saves. Raising LBaseMaxBytes alone makes write
// amplification worse, not better.
const (
	// WriteHeavyLBaseMaxBytes governs how many levels the LSM uses. Pebble picks
	// the base level by walking up from the deepest non-empty level for as long
	// as the projected size of that level exceeds this value, and every step up
	// adds a whole extra level for a byte to be rewritten on its way down.
	//
	// The base level moves in whole steps a LevelMultiplier apart, so a value
	// between two steps buys nothing but a flatter pyramid. This one is sized to
	// put the base at L5 for a mainnet-sized state, against a default of 64MB
	// that puts it at L2.
	WriteHeavyLBaseMaxBytes int64 = 32 << 30

	// WriteHeavyL0CompactionThreshold lets L0 grow to this many sublevels before
	// compacting, which keeps write amplification near one for as long as the
	// backlog lasts and amortises the eventual Lbase rewrite over a much larger
	// batch.
	WriteHeavyL0CompactionThreshold = 24

	// WriteHeavyL0StopWritesThreshold is the hard ceiling on L0 sublevels, past
	// which writes stop. It has to clear the compaction threshold with room to
	// spare or writes stall while the backlog drains.
	WriteHeavyL0StopWritesThreshold = 96

	// WriteHeavyTargetFileSizeL0 is the target size of an L0 file, and only of an
	// L0 file. Pebble derives FlushSplitBytes from it when that is unset, so the
	// 2MB used for regular operation shatters a large memtable flush into
	// hundreds of small tables.
	//
	// The deeper levels deliberately keep their defaults. Compacting into a level
	// rewrites whichever of its tables the incoming keys overlap, so larger
	// tables there coarsen the rewrite unit and raise write amplification.
	WriteHeavyTargetFileSizeL0 int64 = 64 << 20

	// WriteHeavyMemTableCount is the number of memtables kept in flight, with the
	// stop-writes threshold at twice that. Memory is budgeted against the
	// threshold rather than the count, since that is what bounds the footprint.
	WriteHeavyMemTableCount = 4

	// WriteHeavyCacheDivisor is the fraction of the cache allowance left to the
	// block cache, the rest going to memtables. Reads are rare, so the block
	// cache earns little, and memtables come from a separate manual arena in
	// pebble v2 rather than from the block cache, so shrinking one genuinely
	// funds the other.
	WriteHeavyCacheDivisor = 8

	// WriteHeavyMinCacheMB is the floor for the block cache, in megabytes.
	WriteHeavyMinCacheMB = 128
)

// ApplyWriteHeavy re-tunes the config for a write-dominated phase.
//
// Every knob of the profile is set outright rather than filled in where unset,
// since DefaultConfig already populates most of them and a profile that yielded
// to the defaults would be almost entirely inert. Callers pin individual knobs
// with --override instead, which is applied afterwards and therefore wins.
//
// The database has to be reopened without the profile once the phase is over.
// Reads against the L0 backlog it leaves behind are slow, and the backlog only
// drains under the regular settings.
func (c *BenchConfig) ApplyWriteHeavy() {
	// Hand the bulk of the cache allowance to the memtables. Memtables come from
	// a separate manual arena in pebble v2 rather than from the block cache, so
	// shrinking one genuinely funds the other.
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

	// Only L0 gets a larger target. Pebble derives FlushSplitBytes from the L0
	// target and the target also caps the tables a flush emits, so leaving both
	// small shatters one memtable flush into a shower of little tables.
	//
	// The levels below keep their defaults on purpose. A compaction into Li+1 has
	// to rewrite whichever of its tables the incoming keys overlap, so larger
	// tables down there mean a coarser rewrite unit and *more* write
	// amplification, not less: a narrow key range landing in a 2GB table rewrites
	// 2GB, where the same range against 32MB tables rewrites a fraction of that.
	for len(c.Levels) < 7 {
		c.Levels = append(c.Levels, LevelConfig{})
	}
	c.Levels[0].TargetFileSize = WriteHeavyTargetFileSizeL0
	split := c.Levels[0].TargetFileSize
	c.FlushSplitBytes = &split

	// Bloom filters only pay off on reads, yet are rebuilt by every flush and
	// every compaction.
	c.BloomFilterBits = intPtr(0)
	for i := range c.Levels {
		c.Levels[i].NoFilter = true
		c.Levels[i].BloomFilterBits = nil
	}
}
