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
// The values were measured on a mainnet generate-trie run, which has that shape.
// What came out of it is that the levers are not equally worth pulling. File
// sizing dominated: at the default 2MB target the run left ~15k files in L0,
// drove the file-count arm of the L0 score to 30, saturated compaction with tiny
// rewrites and stalled writes for a third of its wall clock. Base level sizing
// and L0 depth, which the profile originally leaned on, both measured worse than
// leaving them near their defaults.
const (
	// WriteHeavyLBaseMaxBytes governs how many levels the LSM uses. Pebble picks
	// the base level by walking up from the deepest non-empty level for as long
	// as the projected size of that level exceeds this value, and every step up
	// adds a whole extra level for a byte to be rewritten on its way down.
	//
	// That is the argument for raising it, and it did not survive measurement on
	// this workload. Against a 64MB base level, raising it to 4GB cost 0.8 bytes per byte accepted on the L0->Lbase step and returned
	// nothing on the levels below it, which stayed flat at ~1.3 either way.
	// Data reaches the lower levels by move compaction in this workload, so
	// there is no fanout to flatten and the deeper base level buys nothing.
	//
	// Keeping it low is what makes the drain cheap: an L0->Lbase compaction
	// writes 1 + lbaseSize/l0Size bytes per byte of L0 it carries down, so the
	// base level wants to stay small relative to L0.
	WriteHeavyLBaseMaxBytes int64 = 4 << 30

	// WriteHeavyL0CompactionThreshold sets how deep L0 grows before draining;
	// the depth reached is roughly half of it, since the fill factor pebble
	// scores L0 by is 2*sublevels/threshold.
	//
	// Depth turned out to be the weakest of these knobs. Raised to 16 it does
	// deepen L0, but a drain still only carried ~3 sublevels: extending one
	// stops at the first table another compaction has already claimed, and at
	// this ingest rate many are. Depth also only pays alongside a large base
	// level to amortise, which measured worse overall.
	WriteHeavyL0CompactionThreshold = 4

	// WriteHeavyL0StopWritesThreshold is the hard ceiling on L0 sublevels, past
	// which writes stop. It has to clear the compaction threshold with room to
	// spare or writes stall while the backlog drains.
	WriteHeavyL0StopWritesThreshold = 24

	// WriteHeavyTargetFileSizeL0 is the target size of an L0 file, and only of an
	// L0 file. Pebble derives FlushSplitBytes from it when that is unset, so the
	// 2MB used for regular operation shatters a large memtable flush into
	// hundreds of small tables. This is the knob that mattered most: raising it
	// took a run from 134 write stalls to none and moved 5.6x more bytes per
	// compaction slot.
	WriteHeavyTargetFileSizeL0 int64 = 16 << 20

	// The levels below L0 also get larger targets than the 4MB-and-doubling
	// default, flattening out at 128MB.
	//
	// This is the one place where the profile now contradicts its own earlier
	// reasoning, which was that a coarser rewrite unit down there raises write
	// amplification: a narrow key range landing in a 2GB table rewrites 2GB,
	// where the same range against 32MB tables rewrites a fraction. That
	// argument still holds for large tables, which is why the ladder stops at
	// 128MB rather than continuing to double. What it misses is the cost at the
	// other end: at the default sizes a mainnet run left ~15k files in L0,
	// which drove the file-count arm of the L0 score to 30, saturated
	// compaction with tiny rewrites and stalled writes for a third of the run.
	// These sizes are the compromise measured to avoid that without coarsening
	// the rewrite unit into the range where it starts costing again.
	WriteHeavyTargetFileSizeLbase  int64 = 32 << 20
	WriteHeavyTargetFileSizeLbase1 int64 = 64 << 20
	WriteHeavyTargetFileSizeDeep   int64 = 128 << 20

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

	// WriteHeavyBloomFilterBits is the bits-per-key of the bloom filters kept on
	// every level except the bottommost. It matches the regular default so the
	// tables written during the burst are indistinguishable, for read purposes,
	// from those the normal-mode phase produces.
	WriteHeavyBloomFilterBits = 10
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

	// Keep bloom filters on every level except the bottommost. They are a write
	// cost during this phase — rebuilt by every flush and every compaction — but
	// the sstables written here outlive it: once the database is reopened under
	// the regular settings the tables are still there, and if they carried no
	// filter the reads that follow would have none until each table is eventually
	// rewritten, which for the settled lower levels is rarely. Paying the filters
	// now buys read-ready tables for the normal-mode phase that follows.
	//
	// The bottommost level is exempt, matching the default policy (bloom on every
	// level but the last). Its filters are the largest, since it holds the bulk of
	// the keys, and the least useful, since a point lookup only reaches it once
	// every level above has been checked and missed. Leaving them off there is
	// where nearly all of the filter's write cost is avoided while the levels that
	// actually gate reads keep theirs.
	c.BloomFilterBits = intPtr(WriteHeavyBloomFilterBits)
	for i := range c.Levels {
		// Disable the filter only on the bottommost level; keep it everywhere
		// above. Stating this on the profile itself, rather than leaning on the
		// db layer's "bloom on every level but the last" default, keeps the
		// intent local and robust to that default changing.
		c.Levels[i].NoFilter = i == len(c.Levels)-1
		c.Levels[i].BloomFilterBits = nil
	}
}
