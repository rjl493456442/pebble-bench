package metrics

import (
	"math"
	"sync"
	"time"
)

// CompactionKind classifies a compaction by where it moves data from/to. We
// keep the three classes we actually want to reason about, rather than
// every (src,dst) pair, because the LSM analysis questions are:
//   - L0→Lbase: the random-hash "passenger" problem (Lbase fan-in per L0 byte)
//   - Lbase→deeper: classical leveled compaction (per-level multiplier cost)
//   - intra-L0: bookkeeping work that doesn't move data downward
type CompactionKind int

const (
	// CompactionL0Lbase is an L0 → Lbase compaction (Output.Level > 0 with at
	// least one input file from L0). This is where the "narrow file → small
	// fan-in" hypothesis lives.
	CompactionL0Lbase CompactionKind = iota

	// CompactionLbasePlus is any non-L0 compaction that moves data toward L6
	// (Output.Level > 0 with no input from L0). It captures the classical
	// leveled per-level cost.
	CompactionLbasePlus

	// CompactionIntraL0 is an L0 → L0 compaction. These do not drain data
	// toward L6, they only reduce L0 sublevel count to keep read amp / stall
	// pressure manageable. The bytes written here are "wasted" relative to
	// total write amp.
	CompactionIntraL0

	numCompactionKinds
)

func (k CompactionKind) String() string {
	switch k {
	case CompactionL0Lbase:
		return "L0->Lbase"
	case CompactionLbasePlus:
		return "Lbase+"
	case CompactionIntraL0:
		return "intra-L0"
	default:
		return "unknown"
	}
}

// CompactionBucket is the aggregated view of one CompactionKind over a run.
//
// FanIn fields are only populated for kinds where the concept makes sense.
// For CompactionL0Lbase, FanIn is bytes from the Lbase level (the "passenger"
// data being rewritten alongside the L0 input). For CompactionLbasePlus,
// FanIn is bytes from the deeper level (the next level down). intra-L0 has no
// meaningful fan-in (input and output are both L0).
type CompactionBucket struct {
	Count       int64         `json:"count"`
	L0Bytes     uint64        `json:"l0_bytes"`     // input bytes from L0 (zero for non-L0 kinds)
	StartBytes  uint64        `json:"start_bytes"`  // input bytes from the starting (non-output) level for Lbase+ compactions
	FanInBytes  uint64        `json:"fan_in_bytes"` // input bytes from the output level (the "passenger")
	OutputBytes uint64        `json:"output_bytes"`
	Duration    time.Duration `json:"duration_ns"`

	// Per-compaction fan-in ratio = fan_in_bytes / source_input_bytes. For
	// L0→Lbase that's Lbase/L0; for Lbase+ that's next_level/start_level.
	// We keep sum + min + max so we can report mean/min/max without retaining
	// the per-compaction sample list.
	SumFanInRatio float64 `json:"sum_fan_in_ratio"`
	MinFanInRatio float64 `json:"min_fan_in_ratio"`
	MaxFanInRatio float64 `json:"max_fan_in_ratio"`
}

// AvgFanInRatio returns the mean (fan-in / source-input) ratio across all
// compactions in this bucket, or 0 if there were none.
func (b CompactionBucket) AvgFanInRatio() float64 {
	if b.Count == 0 {
		return 0
	}
	return b.SumFanInRatio / float64(b.Count)
}

// CompactionStats is a snapshot of all compaction-tracker buckets.
type CompactionStats struct {
	L0Lbase   CompactionBucket `json:"l0_lbase"`
	LbasePlus CompactionBucket `json:"lbase_plus"`
	IntraL0   CompactionBucket `json:"intra_l0"`
}

// CompactionTracker records aggregated per-compaction-kind statistics
// observed at the Pebble EventListener.CompactionEnd boundary. Safe for
// concurrent use; the EventListener may invoke it from internal goroutines.
type CompactionTracker struct {
	mu      sync.Mutex
	buckets [numCompactionKinds]CompactionBucket
}

// NewCompactionTracker creates a tracker with all min-ratios primed to +Inf
// so the first observation always wins.
func NewCompactionTracker() *CompactionTracker {
	t := &CompactionTracker{}
	for i := range t.buckets {
		t.buckets[i].MinFanInRatio = math.Inf(1)
	}
	return t
}

// Record adds one compaction observation to the tracker. The caller is
// responsible for extracting the relevant byte counts and ratio from the
// pebble.CompactionInfo (the metrics package intentionally has no Pebble
// dependency, so the db package adapts the event for us).
func (t *CompactionTracker) Record(
	kind CompactionKind,
	l0Bytes, startBytes, fanInBytes, outputBytes uint64,
	duration time.Duration,
) {
	if kind < 0 || kind >= numCompactionKinds {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	b := &t.buckets[kind]
	b.Count++
	b.L0Bytes += l0Bytes
	b.StartBytes += startBytes
	b.FanInBytes += fanInBytes
	b.OutputBytes += outputBytes
	b.Duration += duration

	// The ratio is fan-in / source-input. Source for L0→Lbase is the L0 bytes;
	// for Lbase+ it's the starting-level bytes. The caller passes both, we pick
	// whichever is non-zero (kinds use exactly one).
	var source uint64
	switch kind {
	case CompactionL0Lbase:
		source = l0Bytes
	case CompactionLbasePlus:
		source = startBytes
	default:
		return // intra-L0: ratio is not meaningful, skip the rest
	}
	if source == 0 {
		return
	}
	ratio := float64(fanInBytes) / float64(source)
	b.SumFanInRatio += ratio
	if ratio < b.MinFanInRatio {
		b.MinFanInRatio = ratio
	}
	if ratio > b.MaxFanInRatio {
		b.MaxFanInRatio = ratio
	}
}

// Stats returns a defensive copy of the current bucket state. Empty buckets
// have their +Inf sentinel min-ratio replaced with 0 so the result encodes
// cleanly as JSON (encoding/json refuses to emit +Inf/-Inf/NaN).
func (t *CompactionTracker) Stats() CompactionStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := CompactionStats{
		L0Lbase:   t.buckets[CompactionL0Lbase],
		LbasePlus: t.buckets[CompactionLbasePlus],
		IntraL0:   t.buckets[CompactionIntraL0],
	}
	for _, b := range []*CompactionBucket{&out.L0Lbase, &out.LbasePlus, &out.IntraL0} {
		if math.IsInf(b.MinFanInRatio, 1) {
			b.MinFanInRatio = 0
		}
	}
	return out
}
