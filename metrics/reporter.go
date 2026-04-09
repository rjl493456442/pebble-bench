package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Result holds the final benchmark results.
type Result struct {
	Config      interface{}    `json:"config"`
	Benchmark   string         `json:"benchmark"`
	Duration    time.Duration  `json:"duration"`
	Summary     Summary        `json:"summary"`
	PebbleFinal PebbleSnapshot `json:"pebble_final"`
	Ticks       []TickRecord   `json:"ticks,omitempty"`
}

// Summary contains aggregated benchmark statistics.
type Summary struct {
	TotalOps     int64   `json:"total_ops"`
	OpsPerSec    float64 `json:"ops_per_sec"`
	OpsPerSecMin float64 `json:"ops_per_sec_min"`
	OpsPerSecMax float64 `json:"ops_per_sec_max"`
	AvgUs        int64   `json:"avg_us"`
	MinUs        int64   `json:"min_us"`
	P50Us        int64   `json:"p50_us"`
	P95Us        int64   `json:"p95_us"`
	P99Us        int64   `json:"p99_us"`
	P999Us       int64   `json:"p999_us"`
	MaxUs        int64   `json:"max_us"`
	StdDevUs     int64   `json:"std_dev_us"`
}

// TickRecord captures per-second stats for time series.
type TickRecord struct {
	Elapsed   float64 `json:"elapsed_s"`
	OpsPerSec float64 `json:"ops_per_sec"`
	P50Us     int64   `json:"p50_us"`
	P99Us     int64   `json:"p99_us"`
}

// PrintSummary prints a human-readable summary to stdout.
func PrintSummary(r *Result) {
	fmt.Println("\n========== Benchmark Results ==========")
	fmt.Printf("Benchmark:  %s\n", r.Benchmark)
	fmt.Printf("Duration:   %s\n", r.Duration.Round(time.Second))
	fmt.Printf("Total Ops:  %d\n", r.Summary.TotalOps)
	fmt.Printf("Ops/sec:    %.0f (min: %.0f, max: %.0f)\n", r.Summary.OpsPerSec, r.Summary.OpsPerSecMin, r.Summary.OpsPerSecMax)
	fmt.Println()
	fmt.Println("Latency:")
	fmt.Printf("  avg:    %s\n", usToStr(r.Summary.AvgUs))
	fmt.Printf("  min:    %s\n", usToStr(r.Summary.MinUs))
	fmt.Printf("  p50:    %s\n", usToStr(r.Summary.P50Us))
	fmt.Printf("  p95:    %s\n", usToStr(r.Summary.P95Us))
	fmt.Printf("  p99:    %s\n", usToStr(r.Summary.P99Us))
	fmt.Printf("  p99.9:  %s\n", usToStr(r.Summary.P999Us))
	fmt.Printf("  max:    %s\n", usToStr(r.Summary.MaxUs))
	fmt.Printf("  stddev: %s\n", usToStr(r.Summary.StdDevUs))
	fmt.Println()
	fmt.Println("Pebble Metrics:")
	fmt.Printf("  Disk Usage:      %s\n", FormatSize(r.PebbleFinal.DiskUsage))
	fmt.Printf("  Read Amp:        %d\n", r.PebbleFinal.ReadAmplification)
	fmt.Printf("  Compactions:     %d\n", r.PebbleFinal.CompactionCount)
	fmt.Printf("  Flushes:         %d\n", r.PebbleFinal.FlushStats.Count)
	if r.PebbleFinal.FlushStats.Count > 0 {
		fs := r.PebbleFinal.FlushStats
		fmt.Printf("    avg:           %s\n", fs.AvgTime().Round(time.Millisecond))
		fmt.Printf("    min:           %s\n", fs.MinTime.Round(time.Millisecond))
		fmt.Printf("    max:           %s\n", fs.MaxTime.Round(time.Millisecond))
		fmt.Printf("    total bytes:   %s\n", FormatSize(fs.TotalBytes))
	}
	fmt.Printf("  Write Stalls:    %d\n", r.PebbleFinal.WriteStallStats.Count)
	if r.PebbleFinal.WriteStallStats.Count > 0 {
		ws := r.PebbleFinal.WriteStallStats
		fmt.Printf("    avg:           %s\n", ws.AvgTime().Round(time.Millisecond))
		fmt.Printf("    max:           %s\n", ws.MaxTime.Round(time.Millisecond))
		fmt.Printf("    total:         %s\n", ws.TotalTime.Round(time.Millisecond))
	}
	fmt.Printf("  Block Cache:     %d / %d\n", r.PebbleFinal.BlockCacheHits,
		r.PebbleFinal.BlockCacheHits+r.PebbleFinal.BlockCacheMisses)
	fmt.Printf("  Table Cache:     %d / %d\n", r.PebbleFinal.TableCacheHits,
		r.PebbleFinal.TableCacheHits+r.PebbleFinal.TableCacheMisses)
	fmt.Printf("  Filter:          %d / %d\n", r.PebbleFinal.FilterHits,
		r.PebbleFinal.FilterHits+r.PebbleFinal.FilterMisses)
	fmt.Println("=======================================")

	// Render ops/sec chart from tick records
	if len(r.Ticks) >= 2 {
		points := make([]ChartPoint, len(r.Ticks))
		for i, t := range r.Ticks {
			points[i] = ChartPoint{
				Elapsed: time.Duration(t.Elapsed * float64(time.Second)),
				Value:   t.OpsPerSec,
			}
		}
		PrintChart("Ops/sec Over Time", points)
	}
}

// WriteJSON writes results to a JSON file.
func WriteJSON(path string, r *Result) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// WriteMarkdown writes results to a Markdown file.
func WriteMarkdown(path string, r *Result) error {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Benchmark Report: %s\n\n", r.Benchmark))

	// Overview
	b.WriteString("## Overview\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	b.WriteString(fmt.Sprintf("| Benchmark | %s |\n", r.Benchmark))
	b.WriteString(fmt.Sprintf("| Duration | %s |\n", r.Duration.Round(time.Second)))
	b.WriteString(fmt.Sprintf("| Total Ops | %d |\n", r.Summary.TotalOps))
	b.WriteString(fmt.Sprintf("| Ops/sec | %.0f |\n", r.Summary.OpsPerSec))
	b.WriteString(fmt.Sprintf("| Ops/sec (min) | %.0f |\n", r.Summary.OpsPerSecMin))
	b.WriteString(fmt.Sprintf("| Ops/sec (max) | %.0f |\n", r.Summary.OpsPerSecMax))

	// Latency
	b.WriteString("\n## Latency Distribution\n\n")
	b.WriteString("| Percentile | Latency |\n")
	b.WriteString("|------------|--------|\n")
	b.WriteString(fmt.Sprintf("| avg | %s |\n", usToStr(r.Summary.AvgUs)))
	b.WriteString(fmt.Sprintf("| min | %s |\n", usToStr(r.Summary.MinUs)))
	b.WriteString(fmt.Sprintf("| p50 | %s |\n", usToStr(r.Summary.P50Us)))
	b.WriteString(fmt.Sprintf("| p95 | %s |\n", usToStr(r.Summary.P95Us)))
	b.WriteString(fmt.Sprintf("| p99 | %s |\n", usToStr(r.Summary.P99Us)))
	b.WriteString(fmt.Sprintf("| p99.9 | %s |\n", usToStr(r.Summary.P999Us)))
	b.WriteString(fmt.Sprintf("| max | %s |\n", usToStr(r.Summary.MaxUs)))
	b.WriteString(fmt.Sprintf("| stddev | %s |\n", usToStr(r.Summary.StdDevUs)))

	// Pebble metrics
	b.WriteString("\n## Pebble Metrics\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	b.WriteString(fmt.Sprintf("| Disk Usage | %s |\n", FormatSize(r.PebbleFinal.DiskUsage)))
	b.WriteString(fmt.Sprintf("| Read Amplification | %d |\n", r.PebbleFinal.ReadAmplification))
	b.WriteString(fmt.Sprintf("| Compactions | %d |\n", r.PebbleFinal.CompactionCount))

	// Flush metrics
	b.WriteString(fmt.Sprintf("| Flushes | %d |\n", r.PebbleFinal.FlushStats.Count))
	if r.PebbleFinal.FlushStats.Count > 0 {
		fs := r.PebbleFinal.FlushStats
		b.WriteString(fmt.Sprintf("| Flush Avg | %s |\n", fs.AvgTime().Round(time.Millisecond)))
		b.WriteString(fmt.Sprintf("| Flush Min | %s |\n", fs.MinTime.Round(time.Millisecond)))
		b.WriteString(fmt.Sprintf("| Flush Max | %s |\n", fs.MaxTime.Round(time.Millisecond)))
		b.WriteString(fmt.Sprintf("| Flush Total Bytes | %s |\n", FormatSize(fs.TotalBytes)))
	}

	// Write stall metrics
	b.WriteString(fmt.Sprintf("| Write Stalls | %d |\n", r.PebbleFinal.WriteStallStats.Count))
	if r.PebbleFinal.WriteStallStats.Count > 0 {
		ws := r.PebbleFinal.WriteStallStats
		b.WriteString(fmt.Sprintf("| Stall Avg | %s |\n", ws.AvgTime().Round(time.Millisecond)))
		b.WriteString(fmt.Sprintf("| Stall Max | %s |\n", ws.MaxTime.Round(time.Millisecond)))
		b.WriteString(fmt.Sprintf("| Stall Total | %s |\n", ws.TotalTime.Round(time.Millisecond)))
	}

	// Cache and filter stats
	b.WriteString(fmt.Sprintf("| Block Cache | %s |\n", hitRateStr(r.PebbleFinal.BlockCacheHits, r.PebbleFinal.BlockCacheMisses)))
	b.WriteString(fmt.Sprintf("| Table Cache | %s |\n", hitRateStr(r.PebbleFinal.TableCacheHits, r.PebbleFinal.TableCacheMisses)))
	b.WriteString(fmt.Sprintf("| Filter | %s |\n", hitRateStr(r.PebbleFinal.FilterHits, r.PebbleFinal.FilterMisses)))

	b.WriteString(fmt.Sprintf("| MemTable Size | %s |\n", FormatSize(r.PebbleFinal.MemTableSize)))
	b.WriteString(fmt.Sprintf("| Compaction Debt | %s |\n", FormatSize(r.PebbleFinal.CompactionDebt)))

	// Level breakdown
	b.WriteString("\n## Level Breakdown\n\n")
	b.WriteString("| Level | Files | Size |\n")
	b.WriteString("|-------|-------|------|\n")
	for i := range 7 {
		if r.PebbleFinal.LevelFiles[i] > 0 || r.PebbleFinal.LevelSizes[i] > 0 {
			b.WriteString(fmt.Sprintf("| L%d | %d | %s |\n",
				i, r.PebbleFinal.LevelFiles[i], FormatSize(uint64(r.PebbleFinal.LevelSizes[i]))))
		}
	}

	// Time series (if available)
	if len(r.Ticks) > 0 {
		b.WriteString("\n## Time Series\n\n")
		b.WriteString("| Elapsed (s) | Ops/sec | p50 | p99 |\n")
		b.WriteString("|-------------|---------|-----|-----|\n")
		for _, tick := range r.Ticks {
			b.WriteString(fmt.Sprintf("| %.0f | %.0f | %s | %s |\n",
				tick.Elapsed, tick.OpsPerSec, usToStr(tick.P50Us), usToStr(tick.P99Us)))
		}
	}

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// WriteMultiMarkdown writes a comparison of multiple results to a Markdown file.
func WriteMultiMarkdown(path string, results []*Result) error {
	if len(results) == 0 {
		return fmt.Errorf("no results to write")
	}

	var b strings.Builder

	b.WriteString("# Benchmark Comparison Report\n\n")

	// Summary comparison table
	b.WriteString("## Summary\n\n")
	b.WriteString("| Metric |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", r.Benchmark))
	}
	b.WriteString("\n|--------|")
	for range results {
		b.WriteString("--------|")
	}
	b.WriteString("\n")

	// Duration
	b.WriteString("| Duration |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", r.Duration.Round(time.Second)))
	}
	b.WriteString("\n")

	// Total Ops
	b.WriteString("| Total Ops |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %d |", r.Summary.TotalOps))
	}
	b.WriteString("\n")

	// Ops/sec
	b.WriteString("| Ops/sec |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %.0f |", r.Summary.OpsPerSec))
	}
	b.WriteString("\n")

	// Latency rows
	for _, pct := range []struct {
		label string
		fn    func(*Summary) int64
	}{
		{"avg", func(s *Summary) int64 { return s.AvgUs }},
		{"min", func(s *Summary) int64 { return s.MinUs }},
		{"p50", func(s *Summary) int64 { return s.P50Us }},
		{"p95", func(s *Summary) int64 { return s.P95Us }},
		{"p99", func(s *Summary) int64 { return s.P99Us }},
		{"p99.9", func(s *Summary) int64 { return s.P999Us }},
		{"max", func(s *Summary) int64 { return s.MaxUs }},
		{"stddev", func(s *Summary) int64 { return s.StdDevUs }},
	} {
		b.WriteString(fmt.Sprintf("| %s |", pct.label))
		for _, r := range results {
			b.WriteString(fmt.Sprintf(" %s |", usToStr(pct.fn(&r.Summary))))
		}
		b.WriteString("\n")
	}

	// Pebble metrics comparison
	b.WriteString("\n## Pebble Metrics\n\n")
	b.WriteString("| Metric |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", r.Benchmark))
	}
	b.WriteString("\n|--------|")
	for range results {
		b.WriteString("--------|")
	}
	b.WriteString("\n")

	b.WriteString("| Disk Usage |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", FormatSize(r.PebbleFinal.DiskUsage)))
	}
	b.WriteString("\n")

	b.WriteString("| Read Amp |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %d |", r.PebbleFinal.ReadAmplification))
	}
	b.WriteString("\n")

	b.WriteString("| Compactions |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %d |", r.PebbleFinal.CompactionCount))
	}
	b.WriteString("\n")

	b.WriteString("| Write Stalls |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %d |", r.PebbleFinal.WriteStallStats.Count))
	}
	b.WriteString("\n")

	b.WriteString("| Stall Total |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", r.PebbleFinal.WriteStallStats.TotalTime.Round(time.Millisecond)))
	}
	b.WriteString("\n")

	b.WriteString("| Block Cache |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", hitRateStr(r.PebbleFinal.BlockCacheHits, r.PebbleFinal.BlockCacheMisses)))
	}
	b.WriteString("\n")

	b.WriteString("| Table Cache |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", hitRateStr(r.PebbleFinal.TableCacheHits, r.PebbleFinal.TableCacheMisses)))
	}
	b.WriteString("\n")

	b.WriteString("| Filter |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", hitRateStr(r.PebbleFinal.FilterHits, r.PebbleFinal.FilterMisses)))
	}
	b.WriteString("\n")

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// PrintComparison prints a side-by-side comparison of two results to stdout.
func PrintComparison(baseline, current *Result) {
	fmt.Println("\n========== Benchmark Comparison ==========")
	fmt.Printf("%-20s %20s %20s %10s\n", "Metric", "Baseline", "Current", "Diff")
	fmt.Printf("%-20s %20s %20s %10s\n", "------", "--------", "-------", "----")

	fmt.Printf("%-20s %20s %20s %10s\n", "Benchmark", baseline.Benchmark, current.Benchmark, "")
	fmt.Printf("%-20s %20s %20s %10s\n", "Duration", baseline.Duration.Round(time.Second).String(), current.Duration.Round(time.Second).String(), "")
	fmt.Printf("%-20s %20d %20d %10s\n", "Total Ops", baseline.Summary.TotalOps, current.Summary.TotalOps, pctDiff(float64(baseline.Summary.TotalOps), float64(current.Summary.TotalOps)))
	fmt.Printf("%-20s %20.0f %20.0f %10s\n", "Ops/sec", baseline.Summary.OpsPerSec, current.Summary.OpsPerSec, pctDiff(baseline.Summary.OpsPerSec, current.Summary.OpsPerSec))
	fmt.Printf("%-20s %20.0f %20.0f %10s\n", "Ops/sec (min)", baseline.Summary.OpsPerSecMin, current.Summary.OpsPerSecMin, pctDiff(baseline.Summary.OpsPerSecMin, current.Summary.OpsPerSecMin))
	fmt.Printf("%-20s %20.0f %20.0f %10s\n", "Ops/sec (max)", baseline.Summary.OpsPerSecMax, current.Summary.OpsPerSecMax, pctDiff(baseline.Summary.OpsPerSecMax, current.Summary.OpsPerSecMax))

	fmt.Println()
	fmt.Println("Latency:")
	for _, p := range []struct {
		label string
		b, c  int64
	}{
		{"  avg", baseline.Summary.AvgUs, current.Summary.AvgUs},
		{"  min", baseline.Summary.MinUs, current.Summary.MinUs},
		{"  p50", baseline.Summary.P50Us, current.Summary.P50Us},
		{"  p95", baseline.Summary.P95Us, current.Summary.P95Us},
		{"  p99", baseline.Summary.P99Us, current.Summary.P99Us},
		{"  p99.9", baseline.Summary.P999Us, current.Summary.P999Us},
		{"  max", baseline.Summary.MaxUs, current.Summary.MaxUs},
		{"  stddev", baseline.Summary.StdDevUs, current.Summary.StdDevUs},
	} {
		fmt.Printf("%-20s %20s %20s %10s\n", p.label, usToStr(p.b), usToStr(p.c), pctDiff(float64(p.b), float64(p.c)))
	}

	fmt.Println()
	fmt.Println("Pebble Metrics:")
	fmt.Printf("%-20s %20s %20s %10s\n", "  Disk Usage", FormatSize(baseline.PebbleFinal.DiskUsage), FormatSize(current.PebbleFinal.DiskUsage), pctDiff(float64(baseline.PebbleFinal.DiskUsage), float64(current.PebbleFinal.DiskUsage)))
	fmt.Printf("%-20s %20d %20d %10s\n", "  Read Amp", baseline.PebbleFinal.ReadAmplification, current.PebbleFinal.ReadAmplification, pctDiff(float64(baseline.PebbleFinal.ReadAmplification), float64(current.PebbleFinal.ReadAmplification)))
	fmt.Printf("%-20s %20d %20d %10s\n", "  Compactions", baseline.PebbleFinal.CompactionCount, current.PebbleFinal.CompactionCount, pctDiff(float64(baseline.PebbleFinal.CompactionCount), float64(current.PebbleFinal.CompactionCount)))
	fmt.Printf("%-20s %20d %20d %10s\n", "  Flushes", baseline.PebbleFinal.FlushStats.Count, current.PebbleFinal.FlushStats.Count, pctDiff(float64(baseline.PebbleFinal.FlushStats.Count), float64(current.PebbleFinal.FlushStats.Count)))
	bAvg := float64(baseline.PebbleFinal.FlushStats.AvgTime().Milliseconds())
	cAvg := float64(current.PebbleFinal.FlushStats.AvgTime().Milliseconds())
	fmt.Printf("%-20s %18.0fms %18.0fms %10s\n", "  Flush Avg", bAvg, cAvg, pctDiff(bAvg, cAvg))
	fmt.Printf("%-20s %20d %20d %10s\n", "  Write Stalls", baseline.PebbleFinal.WriteStallStats.Count, current.PebbleFinal.WriteStallStats.Count, pctDiff(float64(baseline.PebbleFinal.WriteStallStats.Count), float64(current.PebbleFinal.WriteStallStats.Count)))
	bStall := float64(baseline.PebbleFinal.WriteStallStats.TotalTime.Milliseconds())
	cStall := float64(current.PebbleFinal.WriteStallStats.TotalTime.Milliseconds())
	fmt.Printf("%-20s %18.0fms %18.0fms %10s\n", "  Stall Total", bStall, cStall, pctDiff(bStall, cStall))
	fmt.Printf("%-20s %20d %20d %10s\n", "  Block Cache Hits", baseline.PebbleFinal.BlockCacheHits, current.PebbleFinal.BlockCacheHits, pctDiff(float64(baseline.PebbleFinal.BlockCacheHits), float64(current.PebbleFinal.BlockCacheHits)))
	fmt.Printf("%-20s %20d %20d %10s\n", "  Table Cache Hits", baseline.PebbleFinal.TableCacheHits, current.PebbleFinal.TableCacheHits, pctDiff(float64(baseline.PebbleFinal.TableCacheHits), float64(current.PebbleFinal.TableCacheHits)))
	fmt.Printf("%-20s %20d %20d %10s\n", "  Filter Hits", baseline.PebbleFinal.FilterHits, current.PebbleFinal.FilterHits, pctDiff(float64(baseline.PebbleFinal.FilterHits), float64(current.PebbleFinal.FilterHits)))
	fmt.Println("==========================================")
}

// pctDiff returns a formatted percentage change string.
func pctDiff(baseline, current float64) string {
	if baseline == 0 {
		if current == 0 {
			return "0.0%"
		}
		return "+Inf%"
	}
	diff := (current - baseline) / baseline * 100
	if diff > 0 {
		return fmt.Sprintf("+%.1f%%", diff)
	}
	return fmt.Sprintf("%.1f%%", diff)
}

func hitRateStr(hits, misses int64) string {
	total := hits + misses
	if total == 0 {
		return "0 / 0 (0.0%)"
	}
	rate := float64(hits) / float64(total) * 100
	return fmt.Sprintf("%d / %d (%.1f%%)", hits, total, rate)
}

func usToStr(us int64) string {
	if us >= 1000000 {
		return fmt.Sprintf("%.2fs", float64(us)/1000000)
	}
	if us >= 1000 {
		return fmt.Sprintf("%.2fms", float64(us)/1000)
	}
	return fmt.Sprintf("%dus", us)
}
