package metrics

import runtimemetrics "runtime/metrics"

// CPU-time sample names. The runtime reports a per-class breakdown of the
// total CPU time available to the process since it started; subtracting the
// idle class from the total leaves the time actually spent running, which is
// what we want. Reading it this way rather than from getrusage keeps the
// measurement portable and confined to this process.
const (
	cpuTotalMetric = "/cpu/classes/total:cpu-seconds"
	cpuIdleMetric  = "/cpu/classes/idle:cpu-seconds"
)

// CPUSeconds returns the CPU time this process has consumed since it started,
// across all threads. Take it at both ends of a run and subtract to get the
// run's own consumption.
//
// Divided by the run's wall time it gives the number of cores kept busy, which
// is what separates a run bounded by compaction from one bounded by the disk:
// the same throughput at four busy cores and at half a core are two different
// problems, and only the second one is fixed by a faster device.
func CPUSeconds() float64 {
	samples := []runtimemetrics.Sample{
		{Name: cpuTotalMetric},
		{Name: cpuIdleMetric},
	}
	runtimemetrics.Read(samples)
	for i := range samples {
		if samples[i].Value.Kind() != runtimemetrics.KindFloat64 {
			// The metric is unsupported on this runtime; reporting zero lets
			// callers render the breakdown as unavailable rather than wrong.
			return 0
		}
	}
	busy := samples[0].Value.Float64() - samples[1].Value.Float64()
	if busy < 0 {
		return 0
	}
	return busy
}
