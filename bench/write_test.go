package bench

import (
	"bytes"
	"math/rand"
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
		in    string
		want  int
		mixed bool
		err   bool
	}{{"", 0, false, false}, {"random", 0, false, false}, {"sorted", 16, false, false}, {"sorted:4", 4, false, false},
		{"snapsync", 16, true, false}, {"snapsync:8", 8, true, false}, {"sorted:0", 0, false, true}, {"zigzag", 0, false, true}} {
		got, mixed, err := parseKeyPattern(c.in)
		if (err != nil) != c.err || got != c.want || mixed != c.mixed {
			t.Fatalf("parseKeyPattern(%q) = %d, %v, %v", c.in, got, mixed, err)
		}
	}
	// Entropy: the leading share is random, the tail zero.
	v := valueOf(rand.New(rand.NewSource(1)), 100, 0.7)
	if len(v) != 100 || !bytes.Equal(v[70:], make([]byte, 30)) || bytes.Equal(v[:70], make([]byte, 70)) {
		t.Fatalf("valueOf entropy 0.7 wrong: %x", v)
	}
}
