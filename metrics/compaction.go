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
//
// Two ratios are reported per compaction:
//
//   - FanInRatio  = fan_in_bytes / source_input_bytes  ("WA accounting view")
//     Tells you what (1 + ratio) write-amp this step contributed per source
//     byte. Independent of the destination level's absolute size.
//
//   - PctOfOutput = fan_in_bytes / output_level_total_bytes  ("geometry view")
//     Tells you what fraction of the destination level was touched by this
//     compaction. Bounded in [0, 1], maps directly to compaction key-range
//     coverage for random-hash workloads. Only meaningful when we have a
//     non-zero estimate of the destination level's total size at compaction
//     time; otherwise the sample is skipped (PctCount records how many
//     observations contributed to Sum/Min/Max for PctOfOutput).
type CompactionBucket struct {
	Count       int64         `json:"count"`
	L0Bytes     uint64        `json:"l0_bytes"`     // input bytes from L0 (zero for non-L0 kinds)
	StartBytes  uint64        `json:"start_bytes"`  // input bytes from the starting (non-output) level for Lbase+ compactions
	FanInBytes  uint64        `json:"fan_in_bytes"` // input bytes from the output level (the "passenger")
	OutputBytes uint64        `json:"output_bytes"`
	Duration    time.Duration `json:"duration_ns"`

	// Per-compaction WA ratio = fan_in / source_input.
	SumFanInRatio float64 `json:"sum_fan_in_ratio"`
	MinFanInRatio float64 `json:"min_fan_in_ratio"`
	MaxFanInRatio float64 `json:"max_fan_in_ratio"`

	// Per-compaction geometric coverage = fan_in / output_level_total. Only
	// recorded when the destination level had a non-zero size at compaction
	// time (otherwise the sample is undefined).
	PctCount   int64   `json:"pct_count"`
	SumPct     float64 `json:"sum_pct"`
	MinPct     float64 `json:"min_pct"`
	MaxPct     float64 `json:"max_pct"`
}

// AvgFanInRatio returns the mean (fan-in / source-input) ratio across all
// compactions in this bucket, or 0 if there were none.
func (b CompactionBucket) AvgFanInRatio() float64 {
	if b.Count == 0 {
		return 0
	}
	return b.SumFanInRatio / float64(b.Count)
}

// AvgPctOfOutput returns the mean (fan-in / output-level-total) across all
// compactions where the destination level size was known to be non-zero, or
// 0 if there were no such samples.
func (b CompactionBucket) AvgPctOfOutput() float64 {
	if b.PctCount == 0 {
		return 0
	}
	return b.SumPct / float64(b.PctCount)
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
//
// The tracker also holds a recently-sampled snapshot of per-level total bytes
// (pushed in by the Collector every few seconds) so that each recorded
// compaction can compute fan-in / destination-level-total — the geometric
// "what fraction of Lbase did this compaction touch" view.
type CompactionTracker struct {
	mu         sync.Mutex
	buckets    [numCompactionKinds]CompactionBucket
	levelBytes [7]uint64 // most recent snapshot of per-level total bytes
}

// NewCompactionTracker creates a tracker with all min-ratios primed to +Inf
// so the first observation always wins.
func NewCompactionTracker() *CompactionTracker {
	t := &CompactionTracker{}
	for i := range t.buckets {
		t.buckets[i].MinFanInRatio = math.Inf(1)
		t.buckets[i].MinPct = math.Inf(1)
	}
	return t
}

// SetLevelBytes pushes in a fresh snapshot of per-level total bytes (typically
// from the collector's periodic db.Metrics() poll). Subsequent Record calls
// use this snapshot to compute the per-compaction fan-in percentage; up to
// the collector's tick interval of staleness is acceptable since Lbase total
// size changes slowly under steady-state writes.
func (t *CompactionTracker) SetLevelBytes(sizes [7]int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range sizes {
		if sizes[i] < 0 {
			t.levelBytes[i] = 0
		} else {
			t.levelBytes[i] = uint64(sizes[i])
		}
	}
}

// Record adds one compaction observation to the tracker. outputLevel is the
// destination level (used to look up the most recent total-bytes snapshot for
// the fan-in percentage). The caller is responsible for extracting the
// relevant byte counts from the pebble.CompactionInfo (the metrics package
// intentionally has no Pebble dependency, so the db package adapts the event
// for us).
func (t *CompactionTracker) Record(
	kind CompactionKind,
	outputLevel int,
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

	// intra-L0 has no destination level distinct from its source, so neither
	// ratio is meaningful here. Stop after the byte accumulators.
	if kind == CompactionIntraL0 {
		return
	}

	// WA ratio = fan-in / source-input.
	var source uint64
	switch kind {
	case CompactionL0Lbase:
		source = l0Bytes
	case CompactionLbasePlus:
		source = startBytes
	}
	if source > 0 {
		ratio := float64(fanInBytes) / float64(source)
		b.SumFanInRatio += ratio
		if ratio < b.MinFanInRatio {
			b.MinFanInRatio = ratio
		}
		if ratio > b.MaxFanInRatio {
			b.MaxFanInRatio = ratio
		}
	}

	// Geometric pct = fan-in / destination-level-total. Skip when the
	// destination level total isn't known (e.g. the very first compactions
	// before the collector has pushed in a snapshot, or a freshly-created
	// destination level). PctCount tracks how many samples actually
	// contributed so the average is well-defined.
	if outputLevel >= 0 && outputLevel < len(t.levelBytes) {
		dst := t.levelBytes[outputLevel]
		if dst > 0 {
			pct := float64(fanInBytes) / float64(dst)
			b.PctCount++
			b.SumPct += pct
			if pct < b.MinPct {
				b.MinPct = pct
			}
			if pct > b.MaxPct {
				b.MaxPct = pct
			}
		}
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
		if math.IsInf(b.MinPct, 1) {
			b.MinPct = 0
		}
	}
	return out
}
