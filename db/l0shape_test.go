package db

import "testing"

func TestL0Shape(t *testing.T) {
	k := func(s string) []byte { return []byte(s) }
	// Three sublevels. Sublevel 1 sits on sublevel 0's grid; the one file in
	// sublevel 2 is staggered by half a cell and straddles two below.
	levels := [][]placed{
		{{k("a"), k("c")}, {k("d"), k("f")}, {k("g"), k("i")}},
		{{k("a"), k("c")}, {k("d"), k("f")}, {k("g"), k("i")}},
		{{k("b"), k("e")}},
	}
	got := l0Shape(levels)
	if got.Files != 7 || got.Measured != 4 {
		t.Fatalf("files=%d measured=%d", got.Files, got.Measured)
	}
	// Sublevel 1: three files at fanout 1, all on a shared edge. Sublevel 2:
	// one file at fanout 2, not on an edge. (3*1+2)/4 = 1.25, 3/4 aligned.
	if got.StepFanout != 1.25 || got.StepFanoutMax != 2 || got.AlignedStarts != 0.75 {
		t.Fatalf("fanout=%v max=%d aligned=%v", got.StepFanout, got.StepFanoutMax, got.AlignedStarts)
	}
	if l0Shape(nil).Measured != 0 || l0Shape(levels[:1]).Measured != 0 {
		t.Fatal("nothing to measure with fewer than two sublevels")
	}
}
