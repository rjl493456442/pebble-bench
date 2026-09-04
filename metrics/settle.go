package metrics

import "time"

// SettleResult records the wait, after the workers stopped, for compaction to
// catch up with what they left behind.
//
// A run that ends with a backlog has not paid for all of its writes yet: the
// rewrites that backlog implies happen after the clock stops, and a snapshot
// taken at that moment credits the run with a write amplification it did not
// earn. On a write-heavy profile the backlog can be most of the data. Waiting
// makes the numbers comparable across profiles, and the wait itself is a
// result: for a node it is the gap between the sync finishing and the store
// being back to steady state.
type SettleResult struct {
	Duration   time.Duration `json:"duration"`
	TimedOut   bool          `json:"timed_out"`
	DebtBefore uint64        `json:"debt_before"`
	DebtAfter  uint64        `json:"debt_after"`
}
