package metrics

import "testing"

// The rows are only worth printing if they account for the total, so hold them
// to it.
func TestStageBreakdownAccountsForTotal(t *testing.T) {
	m := &DBMetrics{
		WALBytesIn: 1000,
		// What pebble reports as WAL.BytesWritten: the logical bytes, plus the
		// 50 intra-L0 bytes it folds into Levels[0].TableBytesIn, plus a little
		// live WAL. The breakdown must not read this as the WAL row.
		WALBytesWritten: 1000 + 50 + 10,
		BaseLevel:       3,
	}
	m.Levels[0] = LevelStat{BytesFlushed: 900, BytesCompacted: 50} // flush + intra-L0
	m.Levels[2] = LevelStat{BytesCompacted: 300}                   // a former base level
	m.Levels[3] = LevelStat{BytesCompacted: 800}                   // the base level
	m.Levels[5] = LevelStat{BytesCompacted: 400}                   // below it
	m.Levels[6] = LevelStat{BytesCompacted: 600}
	// What the adapter reports: every level's bytes, plus the logical WAL bytes.
	m.BytesWritten = 900 + 50 + 300 + 800 + 400 + 600 + 1000

	s := BuildStageBreakdown(m)
	if s.Residual != 0 {
		t.Fatalf("adapter's BytesWritten and the rows disagree by %d", s.Residual)
	}
	if s.Total != m.BytesWritten {
		t.Fatalf("total %d, adapter reported %d", s.Total, m.BytesWritten)
	}
	for _, c := range []struct {
		name string
		got  uint64
		want uint64
	}{
		{"WAL", s.WAL, 1000},
		{"flush", s.FlushToL0, 900},
		{"intra-L0", s.IntraL0, 50},
		{"above Lbase", s.AboveLbase, 300},
		{"L0->Lbase", s.L0ToLbase, 800},
		{"below Lbase", s.LbaseToBot, 1000},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if amp := s.WriteAmp(); amp != 4.05 {
		t.Errorf("write amp = %v, want 4.05", amp)
	}
}
