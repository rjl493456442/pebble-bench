package bench

import (
	"bytes"
	"testing"
)

func TestSortedKeys(t *testing.T) {
	const streams = 16
	// Strictly increasing within a stream.
	for r := range streams {
		prev := sortedKey(r, streams, 0)
		for n := uint64(1); n < 2000; n++ {
			k := sortedKey(r, streams, n)
			if len(k) != 32 || bytes.Compare(prev, k) >= 0 {
				t.Fatalf("stream %d: key %d not increasing", r, n)
			}
			prev = k
		}
	}
	// Disjoint across streams: every key of stream r sorts below every key of
	// stream r+1, however far along either has got.
	for r := 0; r+1 < streams; r++ {
		if bytes.Compare(sortedKey(r, streams, 1<<40), sortedKey(r+1, streams, 0)) >= 0 {
			t.Fatalf("streams %d and %d interleave", r, r+1)
		}
	}
	for _, c := range []struct {
		in   string
		want int
		err  bool
	}{{"", 0, false}, {"random", 0, false}, {"sorted", 16, false}, {"sorted:4", 4, false}, {"sorted:0", 0, true}, {"zigzag", 0, true}} {
		got, err := parseKeyPattern(c.in)
		if (err != nil) != c.err || got != c.want {
			t.Fatalf("parseKeyPattern(%q) = %d, %v", c.in, got, err)
		}
	}
}
