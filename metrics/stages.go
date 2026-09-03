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
	WAL        uint64 `json:"wal"`          // physical WAL cost, framing and padding included
	FlushToL0  uint64 `json:"flush_to_l0"`  // sstable bytes produced by flushes
	IntraL0    uint64 `json:"intra_l0"`     // rewritten within L0, pure overhead
	AboveLbase uint64 `json:"above_lbase"`  // written into levels shallower than the base level
	L0ToLbase  uint64 `json:"l0_to_lbase"`  // written into the base level, ie the L0 drain
	LbaseToBot uint64 `json:"lbase_to_bot"` // written into every level below the base
	Total      uint64 `json:"total"`        // physical bytes written, the numerator of write amp
	Residual   int64  `json:"residual"`     // Total less the rows above; zero when they account for it all
}

// BuildStageBreakdown decomposes m's write volume.
//
// Two details are easy to get wrong and are worth stating, because getting
// either wrong leaves the rows short of the total by a margin large enough to
// change a conclusion:
//
// The WAL row is the physical cost, not the logical bytes accepted. The column
// accounts for what reached the disk, and framing and padding are part of
// that; the logical figure is the denominator of the ratios, not a row.
//
// A level shallower than the current base level belongs to no stage at first
// glance, but it holds bytes all the same: the base level moves down as a
// database grows, and whichever level used to be the base keeps everything
// that passed through it while it was. On a long write-heavy run that is not a
// rounding error.
func BuildStageBreakdown(m *DBMetrics) StageBreakdown {
	s := StageBreakdown{
		Accepted: m.WALBytesIn,
		WAL:      m.WALBytesWritten,
		// Not BytesWritten plus the WAL: pebble's Metrics.Total folds the WAL
		// and any ingested bytes into the flushed total, so BytesWritten
		// already carries them and adding them again counts the WAL twice.
		Total: m.BytesWritten,
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
	s.Residual = int64(s.Total) - int64(s.WAL+s.FlushToL0+s.IntraL0+s.AboveLbase+s.L0ToLbase+s.LbaseToBot)
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
