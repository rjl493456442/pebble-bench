package metrics

// StageBreakdown decomposes everything a run wrote to disk into the steps that
// wrote it, so that a write-amplification figure can be attributed rather than
// merely quoted.
//
// The rows come from each level's own byte counters, not from compaction
// inputs. A compaction drops overwritten keys and tombstones as it merges, so
// its inputs overstate what reaches the disk; the level counters are what
// actually landed. Taken this way the rows sum to Total exactly, and Residual
// exists to prove it.
type StageBreakdown struct {
	Accepted   uint64 `json:"accepted"`     // logical bytes handed to the database
	WAL        uint64 `json:"wal"`          // WAL cost, as the logical bytes; pebble tracks no physical figure
	FlushToL0  uint64 `json:"flush_to_l0"`  // sstable bytes produced by flushes
	IntraL0    uint64 `json:"intra_l0"`     // rewritten within L0, pure overhead
	AboveLbase uint64 `json:"above_lbase"`  // written into levels shallower than the base level
	L0ToLbase  uint64 `json:"l0_to_lbase"`  // written into the base level, ie the L0 drain
	LbaseToBot uint64 `json:"lbase_to_bot"` // written into every level below the base
	Total      uint64 `json:"total"`        // the rows above summed: bytes written, the numerator of write amp
	Residual   int64  `json:"residual"`     // the adapter's headline BytesWritten less Total; non-zero means the two disagree
}

// BuildStageBreakdown decomposes m's write volume.
//
// Total is built from the same level counters as the rows, so the rows account
// for it by construction. Residual compares that against the headline
// BytesWritten the db adapter reported, which is a genuine cross-check: the
// two are computed separately, and a result file written by an adapter that
// summed differently, as earlier versions did, shows the difference here
// instead of silently inheriting it.
//
// Two details are easy to get wrong and are worth stating, because getting
// either wrong leaves the rows short of the total by a margin large enough to
// change a conclusion:
//
// The WAL row is the logical bytes accepted, and deliberately not pebble's
// WAL.BytesWritten. That field is not a physical figure despite the name: it
// is Levels[0].TableBytesIn plus the live WAL size, and every intra-L0
// compaction adds its input to TableBytesIn because L0 is its output level.
// Read as the WAL row it would count intra-L0 bytes a second time, on top of
// the intra-L0 row, and on a run that leaned on intra-L0 it overstated the
// total by that entire row. Pebble keeps no physical WAL byte count, and the
// framing the logical figure omits is well under a percent.
//
// A level shallower than the current base level belongs to no stage at first
// glance, but it holds bytes all the same: the base level moves down as a
// database grows, and whichever level used to be the base keeps everything
// that passed through it while it was. On a long write-heavy run that is not a
// rounding error.
func BuildStageBreakdown(m *DBMetrics) StageBreakdown {
	s := StageBreakdown{
		Accepted: m.WALBytesIn,
		WAL:      m.WALBytesIn,
	}
	s.FlushToL0 = m.Levels[0].BytesFlushed
	s.IntraL0 = m.Levels[0].BytesCompacted
	for level := 1; level < len(m.Levels); level++ {
		written := m.Levels[level].BytesCompacted + m.Levels[level].BytesFlushed
		switch {
		case level == m.BaseLevel:
			s.L0ToLbase += written
		case m.BaseLevel >= 0 && level > m.BaseLevel:
			s.LbaseToBot += written
		default:
			s.AboveLbase += written
		}
	}
	s.Total = s.WAL + s.FlushToL0 + s.IntraL0 + s.AboveLbase + s.L0ToLbase + s.LbaseToBot
	s.Residual = int64(m.BytesWritten) - int64(s.Total)
	return s
}

// PerByte returns v as a share of the logical bytes accepted, which is the
// unit every row of the breakdown is read in: bytes written per byte the
// caller handed over.
func (s StageBreakdown) PerByte(v uint64) float64 {
	if s.Accepted == 0 {
		return 0
	}
	return float64(v) / float64(s.Accepted)
}

// WriteAmp returns the physical bytes written per logical byte accepted.
func (s StageBreakdown) WriteAmp() float64 { return s.PerByte(s.Total) }
