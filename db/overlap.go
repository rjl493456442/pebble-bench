package db

import (
	"bytes"
	"sort"
)

// overlapDepth returns the largest number of the given key ranges that cover
// any one point, with the bounds taken as inclusive at both ends. Applied to
// the L0 tables a compaction consumed, that is the number of sublevels the
// compaction drained: files within one L0 sublevel never overlap, so a point
// covered by n files was covered by n distinct sublevels.
//
// starts and ends are parallel: ends[i] is the upper bound of the range whose
// lower bound is starts[i]. Both slices are sorted in place, which is why they
// are passed separately rather than as pairs — the sweep only ever needs to
// know how many ranges have opened and how many have closed by a given point,
// never which range an endpoint belonged to.
func overlapDepth(starts, ends [][]byte) int {
	if len(starts) != len(ends) || len(starts) == 0 {
		return 0
	}
	sort.Slice(starts, func(i, j int) bool { return bytes.Compare(starts[i], starts[j]) < 0 })
	sort.Slice(ends, func(i, j int) bool { return bytes.Compare(ends[i], ends[j]) < 0 })

	var depth, retired int
	for i, start := range starts {
		// Retire every range that ended before this one began. The comparison
		// is strict because the bounds are inclusive: a range ending exactly on
		// this start still covers the point and is still open.
		for retired < len(ends) && bytes.Compare(ends[retired], start) < 0 {
			retired++
		}
		if open := i + 1 - retired; open > depth {
			depth = open
		}
	}
	return depth
}
