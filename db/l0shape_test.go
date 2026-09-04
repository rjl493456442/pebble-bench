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
	got := l0Shape(levels, k("a"), k("i"))
	if got.Files != 7 || got.Measured != 4 {
		t.Fatalf("files=%d measured=%d", got.Files, got.Measured)
	}
	// Sublevel 1: three files at fanout 1, all on a shared edge. Sublevel 2:
	// one file at fanout 2, not on an edge. (3*1+2)/4 = 1.25, 3/4 aligned.
	if got.StepFanout != 1.25 || got.StepFanoutMax != 2 || got.AlignedStarts != 0.75 {
		t.Fatalf("fanout=%v max=%d aligned=%v", got.StepFanout, got.StepFanoutMax, got.AlignedStarts)
	}
	// The three grid-aligned files widen by 1.0; b-e grows to a-f once its
	// two neighbours below come along.
	grow := keyFraction(k("a"), k("f"), k("a"), k("i")) / keyFraction(k("b"), k("e"), k("a"), k("i"))
	want := (3*1.0 + grow) / 4
	if d := got.StepWidening - want; d > 1e-9 || d < -1e-9 || got.StepWideningMax != grow {
		t.Fatalf("widening=%v want %v (max %v want %v)", got.StepWidening, want, got.StepWideningMax, grow)
	}
	// A nested grid: a 2-cell file over two 1-cell files has fanout 2 but no
	// widening, which is the case that tells the two apart.
	nested := [][]placed{
		{{k("a"), k("b")}, {k("c"), k("d")}},
		{{k("a"), k("d")}},
	}
	if n := l0Shape(nested, k("a"), k("d")); n.StepFanout != 2 || n.StepWidening != 1 {
		t.Fatalf("nested grid: fanout=%v widening=%v", n.StepFanout, n.StepWidening)
	}
	if l0Shape(nil, nil, nil).Measured != 0 || l0Shape(levels[:1], k("a"), k("i")).Measured != 0 {
		t.Fatal("nothing to measure with fewer than two sublevels")
	}
}
