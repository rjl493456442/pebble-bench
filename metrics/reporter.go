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
	OpSummaries []OpSummary    `json:"op_summaries,omitempty"`
	PebbleFinal PebbleSnapshot `json:"pebble_final"`
	ReadAmpAvg  float64        `json:"read_amp_avg"`
	ReadAmpMax  int            `json:"read_amp_max"`
	Ticks       []TickRecord   `json:"ticks,omitempty"`
}

// OpSummary holds a latency summary for a single operation type (e.g. the
// "read" and "write" halves of the mixed benchmark).
type OpSummary struct {
	Name    string  `json:"name"`
	Summary Summary `json:"summary"`
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

	// Per-operation breakdown (e.g. read vs write for the mixed benchmark).
	if len(r.OpSummaries) > 1 {
		fmt.Println()
		fmt.Println("Latency by operation:")
		for _, op := range r.OpSummaries {
			fmt.Printf("  %-6s ops=%d  ops/sec=%.0f  p50=%s  p99=%s  p99.9=%s  max=%s\n",
				op.Name+":", op.Summary.TotalOps, op.Summary.OpsPerSec,
				usToStr(op.Summary.P50Us), usToStr(op.Summary.P99Us),
				usToStr(op.Summary.P999Us), usToStr(op.Summary.MaxUs))
		}
	}

	fmt.Println()
	fmt.Println("Pebble Metrics:")
	fmt.Printf("  Disk Usage:      %s\n", FormatSize(r.PebbleFinal.DiskUsage))
	fmt.Printf("  Write Amp:       %.2f\n", r.PebbleFinal.WriteAmp)
	fmt.Printf("  Bytes Written:   %s (read %s, logical-in %s)\n",
		FormatSize(r.PebbleFinal.BytesWritten), FormatSize(r.PebbleFinal.BytesRead), FormatSize(r.PebbleFinal.BytesIn))
	fmt.Printf("  Read Amp:        %d (avg %.1f, max %d)\n",
		r.PebbleFinal.ReadAmplification, r.ReadAmpAvg, r.ReadAmpMax)
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
	printSyncStats(r.PebbleFinal.SyncStats)
	printReadStats(r.PebbleFinal.ReadStats)
	printCompactionStats(r.PebbleFinal.CompactionStats)
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

	// Per-operation latency breakdown
	if len(r.OpSummaries) > 1 {
		b.WriteString("\n## Latency by Operation\n\n")
		b.WriteString("| Operation | Ops | Ops/sec | p50 | p99 | p99.9 | max |\n")
		b.WriteString("|-----------|-----|---------|-----|-----|-------|-----|\n")
		for _, op := range r.OpSummaries {
			b.WriteString(fmt.Sprintf("| %s | %d | %.0f | %s | %s | %s | %s |\n",
				op.Name, op.Summary.TotalOps, op.Summary.OpsPerSec,
				usToStr(op.Summary.P50Us), usToStr(op.Summary.P99Us),
				usToStr(op.Summary.P999Us), usToStr(op.Summary.MaxUs)))
		}
	}

	// Pebble metrics
	b.WriteString("\n## Pebble Metrics\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	b.WriteString(fmt.Sprintf("| Disk Usage | %s |\n", FormatSize(r.PebbleFinal.DiskUsage)))
	b.WriteString(fmt.Sprintf("| Write Amplification | %.2f |\n", r.PebbleFinal.WriteAmp))
	b.WriteString(fmt.Sprintf("| Bytes Written | %s |\n", FormatSize(r.PebbleFinal.BytesWritten)))
	b.WriteString(fmt.Sprintf("| Bytes Read (compaction) | %s |\n", FormatSize(r.PebbleFinal.BytesRead)))
	b.WriteString(fmt.Sprintf("| Bytes In (logical) | %s |\n", FormatSize(r.PebbleFinal.BytesIn)))
	b.WriteString(fmt.Sprintf("| Read Amplification (final) | %d |\n", r.PebbleFinal.ReadAmplification))
	b.WriteString(fmt.Sprintf("| Read Amplification (avg / max) | %.1f / %d |\n", r.ReadAmpAvg, r.ReadAmpMax))
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

	// Sync-call counts and timings (fsync / fdatasync / sync_file_range).
	for _, op := range []struct {
		label string
		stats IOStat
	}{
		{"fsync", r.PebbleFinal.SyncStats.Sync},
		{"fdatasync", r.PebbleFinal.SyncStats.SyncData},
		{"sync_file_range", r.PebbleFinal.SyncStats.SyncTo},
		{"fallocate", r.PebbleFinal.SyncStats.Preallocate},
	} {
		b.WriteString(fmt.Sprintf("| %s (count / avg / max) | %d / %s / %s |\n",
			op.label, op.stats.Count, fmtDur(op.stats.AvgTime()), fmtDur(op.stats.MaxTime)))
	}

	// Read-path syscall counts and timings. Counts only reflect reads that miss
	// the block cache and reach the disk; readahead is an async prefetch hint.
	for _, op := range []struct {
		label string
		stats IOStat
	}{
		{"pread", r.PebbleFinal.ReadStats.ReadAt},
		{"read", r.PebbleFinal.ReadStats.Read},
		{"readahead (hint)", r.PebbleFinal.ReadStats.Prefetch},
	} {
		b.WriteString(fmt.Sprintf("| %s (count / avg / max) | %d / %s / %s |\n",
			op.label, op.stats.Count, fmtDur(op.stats.AvgTime()), fmtDur(op.stats.MaxTime)))
	}

	// Per-kind compaction breakdown (count, source bytes, fan-in bytes, ratio).
	// The fan-in ratio is the key number: for L0→Lbase it tells you the
	// "passenger" Lbase bytes pulled in per L0 byte.
	writeCompactionMarkdown(&b, r.PebbleFinal.CompactionStats)

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

	b.WriteString("| Write Amp |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %.2f |", r.PebbleFinal.WriteAmp))
	}
	b.WriteString("\n")

	b.WriteString("| Bytes Written |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", FormatSize(r.PebbleFinal.BytesWritten)))
	}
	b.WriteString("\n")

	b.WriteString("| Read Amp (final) |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %d |", r.PebbleFinal.ReadAmplification))
	}
	b.WriteString("\n")

	b.WriteString("| Read Amp (avg) |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %.1f |", r.ReadAmpAvg))
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

	// VFS syscall counts and average latencies. Each op contributes a count row
	// and an avg row; ops that never fired in any result are omitted.
	b.WriteString("\n## Syscalls (VFS)\n\n")
	b.WriteString("| Syscall |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", r.Benchmark))
	}
	b.WriteString("\n|--------|")
	for range results {
		b.WriteString("--------|")
	}
	b.WriteString("\n")

	for _, op := range []struct {
		label string
		get   func(*Result) IOStat
	}{
		{"fsync", func(r *Result) IOStat { return r.PebbleFinal.SyncStats.Sync }},
		{"fdatasync", func(r *Result) IOStat { return r.PebbleFinal.SyncStats.SyncData }},
		{"sync_file_range", func(r *Result) IOStat { return r.PebbleFinal.SyncStats.SyncTo }},
		{"fallocate", func(r *Result) IOStat { return r.PebbleFinal.SyncStats.Preallocate }},
		{"pread", func(r *Result) IOStat { return r.PebbleFinal.ReadStats.ReadAt }},
		{"read", func(r *Result) IOStat { return r.PebbleFinal.ReadStats.Read }},
		{"readahead", func(r *Result) IOStat { return r.PebbleFinal.ReadStats.Prefetch }},
	} {
		fired := false
		for _, r := range results {
			if op.get(r).Count > 0 {
				fired = true
				break
			}
		}
		if !fired {
			continue
		}
		b.WriteString(fmt.Sprintf("| %s (n) |", op.label))
		for _, r := range results {
			b.WriteString(fmt.Sprintf(" %d |", op.get(r).Count))
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("| %s (avg) |", op.label))
		for _, r := range results {
			b.WriteString(fmt.Sprintf(" %s |", fmtDur(op.get(r).AvgTime())))
		}
		b.WriteString("\n")
	}

	// Per-kind compaction breakdown. The L0→Lbase fan-in ratio is the headline
	// number for "did this tuning change the random-hash passenger problem".
	b.WriteString("\n## Compactions (by kind)\n\n")
	b.WriteString("| Metric |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", r.Benchmark))
	}
	b.WriteString("\n|--------|")
	for range results {
		b.WriteString("--------|")
	}
	b.WriteString("\n")
	for _, k := range []struct {
		label string
		get   func(*Result) CompactionBucket
		ratio bool
	}{
		{"L0->Lbase", func(r *Result) CompactionBucket { return r.PebbleFinal.CompactionStats.L0Lbase }, true},
		{"Lbase+", func(r *Result) CompactionBucket { return r.PebbleFinal.CompactionStats.LbasePlus }, true},
		{"intra-L0", func(r *Result) CompactionBucket { return r.PebbleFinal.CompactionStats.IntraL0 }, false},
	} {
		fired := false
		for _, r := range results {
			if k.get(r).Count > 0 {
				fired = true
				break
			}
		}
		if !fired {
			continue
		}
		b.WriteString(fmt.Sprintf("| %s (n) |", k.label))
		for _, r := range results {
			b.WriteString(fmt.Sprintf(" %d |", k.get(r).Count))
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("| %s (src/op) |", k.label))
		for _, r := range results {
			b.WriteString(fmt.Sprintf(" %s |", FormatSize(avgSourcePerCompaction(k.get(r)))))
		}
		b.WriteString("\n")
		if k.ratio {
			b.WriteString(fmt.Sprintf("| %s (fanin/op) |", k.label))
			for _, r := range results {
				b.WriteString(fmt.Sprintf(" %s |", FormatSize(avgFanInPerCompaction(k.get(r)))))
			}
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("| %s (WA ratio) |", k.label))
			for _, r := range results {
				b.WriteString(fmt.Sprintf(" %.2f |", weightedFanInRatio(k.get(r))))
			}
			b.WriteString("\n")
			// Geometric pct row — only added if at least one result has it.
			hasPct := false
			for _, r := range results {
				if k.get(r).PctCount > 0 {
					hasPct = true
					break
				}
			}
			if hasPct {
				b.WriteString(fmt.Sprintf("| %s (pct of dst) |", k.label))
				for _, r := range results {
					b.WriteString(fmt.Sprintf(" %.2f%% |", k.get(r).AvgPctOfOutput()*100))
				}
				b.WriteString("\n")
			}
		}
	}

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
	fmt.Printf("%-20s %20.2f %20.2f %10s\n", "  Write Amp", baseline.PebbleFinal.WriteAmp, current.PebbleFinal.WriteAmp, pctDiff(baseline.PebbleFinal.WriteAmp, current.PebbleFinal.WriteAmp))
	fmt.Printf("%-20s %20s %20s %10s\n", "  Bytes Written", FormatSize(baseline.PebbleFinal.BytesWritten), FormatSize(current.PebbleFinal.BytesWritten), pctDiff(float64(baseline.PebbleFinal.BytesWritten), float64(current.PebbleFinal.BytesWritten)))
	fmt.Printf("%-20s %20d %20d %10s\n", "  Read Amp (final)", baseline.PebbleFinal.ReadAmplification, current.PebbleFinal.ReadAmplification, pctDiff(float64(baseline.PebbleFinal.ReadAmplification), float64(current.PebbleFinal.ReadAmplification)))
	fmt.Printf("%-20s %20.1f %20.1f %10s\n", "  Read Amp (avg)", baseline.ReadAmpAvg, current.ReadAmpAvg, pctDiff(baseline.ReadAmpAvg, current.ReadAmpAvg))
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

	// VFS syscall counts and average latencies. Ops that never fired on either
	// side (e.g. fdatasync/sync_file_range/fallocate/readahead on macOS) are
	// skipped to keep the table focused.
	fmt.Println()
	fmt.Println("Syscalls (VFS):")
	for _, op := range []struct {
		label string
		b, c  IOStat
	}{
		{"fsync", baseline.PebbleFinal.SyncStats.Sync, current.PebbleFinal.SyncStats.Sync},
		{"fdatasync", baseline.PebbleFinal.SyncStats.SyncData, current.PebbleFinal.SyncStats.SyncData},
		{"sync_file_range", baseline.PebbleFinal.SyncStats.SyncTo, current.PebbleFinal.SyncStats.SyncTo},
		{"fallocate", baseline.PebbleFinal.SyncStats.Preallocate, current.PebbleFinal.SyncStats.Preallocate},
		{"pread", baseline.PebbleFinal.ReadStats.ReadAt, current.PebbleFinal.ReadStats.ReadAt},
		{"read", baseline.PebbleFinal.ReadStats.Read, current.PebbleFinal.ReadStats.Read},
		{"readahead", baseline.PebbleFinal.ReadStats.Prefetch, current.PebbleFinal.ReadStats.Prefetch},
	} {
		if op.b.Count == 0 && op.c.Count == 0 {
			continue
		}
		fmt.Printf("%-20s %20d %20d %10s\n", op.label+" cnt",
			op.b.Count, op.c.Count, pctDiff(float64(op.b.Count), float64(op.c.Count)))
		ba, ca := op.b.AvgTime(), op.c.AvgTime()
		fmt.Printf("%-20s %20s %20s %10s\n", op.label+" avg",
			fmtDur(ba), fmtDur(ca), pctDiff(float64(ba), float64(ca)))
	}

	// Compaction breakdown comparison. For each kind we show:
	//   - count
	//   - average source bytes per compaction (L0 input for L0→Lbase, start
	//     level for Lbase+)
	//   - average fan-in bytes per compaction
	//   - bytes-weighted fan-in ratio (totalFanIn / totalSource)
	// Together these answer "how big was each compaction, and how much
	// passenger data did it pull in".
	fmt.Println()
	fmt.Println("Compactions (by kind):")
	for _, k := range []struct {
		label string
		b, c  CompactionBucket
	}{
		{"L0->Lbase", baseline.PebbleFinal.CompactionStats.L0Lbase, current.PebbleFinal.CompactionStats.L0Lbase},
		{"Lbase+", baseline.PebbleFinal.CompactionStats.LbasePlus, current.PebbleFinal.CompactionStats.LbasePlus},
		{"intra-L0", baseline.PebbleFinal.CompactionStats.IntraL0, current.PebbleFinal.CompactionStats.IntraL0},
	} {
		if k.b.Count == 0 && k.c.Count == 0 {
			continue
		}
		fmt.Printf("%-20s %20d %20d %10s\n", "  "+k.label+" cnt",
			k.b.Count, k.c.Count, pctDiff(float64(k.b.Count), float64(k.c.Count)))
		bAvgSrc := avgSourcePerCompaction(k.b)
		cAvgSrc := avgSourcePerCompaction(k.c)
		fmt.Printf("%-20s %20s %20s %10s\n", "  "+k.label+" src/op",
			FormatSize(bAvgSrc), FormatSize(cAvgSrc), pctDiff(float64(bAvgSrc), float64(cAvgSrc)))
		if k.label != "intra-L0" {
			bAvgFI := avgFanInPerCompaction(k.b)
			cAvgFI := avgFanInPerCompaction(k.c)
			fmt.Printf("%-20s %20s %20s %10s\n", "  "+k.label+" fanin/op",
				FormatSize(bAvgFI), FormatSize(cAvgFI), pctDiff(float64(bAvgFI), float64(cAvgFI)))
			bRatio := weightedFanInRatio(k.b)
			cRatio := weightedFanInRatio(k.c)
			fmt.Printf("%-20s %20.2f %20.2f %10s\n", "  "+k.label+" WA ratio",
				bRatio, cRatio, pctDiff(bRatio, cRatio))
			// Geometric coverage: fan-in / destination-level total. Only
			// rendered when at least one observation contributed; the avg
			// is per-compaction (matches what AvgPctOfOutput returns).
			if k.b.PctCount > 0 || k.c.PctCount > 0 {
				bPct := k.b.AvgPctOfOutput() * 100
				cPct := k.c.AvgPctOfOutput() * 100
				fmt.Printf("%-20s %19.2f%% %19.2f%% %10s\n", "  "+k.label+" pct of dst",
					bPct, cPct, pctDiff(bPct, cPct))
			}
		}
	}
	fmt.Println("==========================================")
}

// avgSourcePerCompaction returns the average source-level bytes per compaction
// in this bucket (L0 bytes for L0→Lbase, start-level bytes for Lbase+, L0
// bytes for intra-L0). Zero when no compactions of this kind occurred.
func avgSourcePerCompaction(b CompactionBucket) uint64 {
	if b.Count == 0 {
		return 0
	}
	source := b.L0Bytes
	if b.StartBytes > 0 {
		source = b.StartBytes
	}
	return source / uint64(b.Count)
}

// avgFanInPerCompaction returns the average fan-in (output-level passenger)
// bytes per compaction. Not meaningful for intra-L0, which has no destination.
func avgFanInPerCompaction(b CompactionBucket) uint64 {
	if b.Count == 0 {
		return 0
	}
	return b.FanInBytes / uint64(b.Count)
}

// weightedFanInRatio is the bytes-weighted fan-in ratio across all compactions
// in the bucket: total fan-in bytes / total source bytes. This differs from
// AvgFanInRatio (the per-compaction mean) — the weighted variant is robust to
// outlier large/small compactions and is the better summary statistic.
func weightedFanInRatio(b CompactionBucket) float64 {
	source := b.L0Bytes
	if b.StartBytes > 0 {
		source = b.StartBytes
	}
	if source == 0 {
		return 0
	}
	return float64(b.FanInBytes) / float64(source)
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

// printSyncStats prints the write-path syscall counts and timings. All lines
// are always shown so that a zero count (e.g. fdatasync and sync_file_range on
// macOS, where they fall back to fsync) is visible rather than silently
// omitted.
func printSyncStats(s SyncStats) {
	fmt.Println("  Write syscalls (VFS):")
	for _, op := range []struct {
		label string
		stats IOStat
	}{
		{"fsync", s.Sync},
		{"fdatasync", s.SyncData},
		{"sync_file_range", s.SyncTo},
		{"fallocate", s.Preallocate},
	} {
		fmt.Printf("    %-16s count=%-8d avg=%-10s max=%s\n",
			op.label, op.stats.Count, fmtDur(op.stats.AvgTime()), fmtDur(op.stats.MaxTime))
	}
}

// printReadStats prints the read-path syscall counts and timings. These count
// only reads that reach the disk (block-cache hits never touch the VFS), so
// pread reflects the actual disk-read syscalls and its avg is the average read
// latency. readahead is an async prefetch *hint* (issued on sequential access),
// not a data transfer, so its timing is the cost of the hint, not read latency.
func printReadStats(s ReadStats) {
	fmt.Println("  Read syscalls (VFS):")
	for _, op := range []struct {
		label string
		stats IOStat
	}{
		{"pread", s.ReadAt},
		{"read", s.Read},
		{"readahead(hint)", s.Prefetch},
	} {
		fmt.Printf("    %-16s count=%-8d avg=%-10s max=%s\n",
			op.label, op.stats.Count, fmtDur(op.stats.AvgTime()), fmtDur(op.stats.MaxTime))
	}
}

// printCompactionStats prints the per-kind compaction breakdown. For the two
// kinds where a fan-in / source ratio is meaningful (L0→Lbase, Lbase+), we
// also show count, average and max — this is what tells you whether a tuning
// change actually moved the per-compaction "passenger" cost. intra-L0 is
// shown bytes-only because it has no destination level to fan-into.
func printCompactionStats(s CompactionStats) {
	fmt.Println("  Compactions (by kind):")
	printCompactionBucket("L0->Lbase", s.L0Lbase, "Lbase/L0")
	printCompactionBucket("Lbase+   ", s.LbasePlus, "next/start")
	if s.IntraL0.Count > 0 {
		fmt.Printf("    %-16s count=%-8d input=%-10s output=%s\n",
			"intra-L0", s.IntraL0.Count,
			FormatSize(s.IntraL0.L0Bytes), FormatSize(s.IntraL0.OutputBytes))
	} else {
		fmt.Printf("    %-16s count=0\n", "intra-L0")
	}
}

// printCompactionBucket prints one row of the compaction-kind table. label is
// the kind name as shown to the user; ratioLabel describes the per-compaction
// WA ratio's numerator/denominator (e.g. "Lbase/L0" for L0→Lbase, where the
// ratio is the passenger Lbase bytes pulled in per L0 byte). When at least
// one observation contributed to the geometric pct (fan-in / destination
// total), a second line shows the pct stats.
func printCompactionBucket(label string, b CompactionBucket, ratioLabel string) {
	if b.Count == 0 {
		fmt.Printf("    %-16s count=0\n", label)
		return
	}
	source := b.L0Bytes
	if b.StartBytes > 0 {
		source = b.StartBytes
	}
	// Sum-based fan-in: total fan-in bytes divided by total source bytes,
	// independent of per-compaction variance. Often more meaningful than the
	// per-compaction mean when compactions vary in size.
	var weightedRatio float64
	if source > 0 {
		weightedRatio = float64(b.FanInBytes) / float64(source)
	}
	fmt.Printf("    %-16s count=%-8d src=%-10s fan-in=%-10s out=%-10s ratio[%s]=%.2f(avg) min=%.2f max=%.2f\n",
		label, b.Count,
		FormatSize(source), FormatSize(b.FanInBytes), FormatSize(b.OutputBytes),
		ratioLabel, weightedRatio, b.MinFanInRatio, b.MaxFanInRatio)
	if b.PctCount > 0 {
		// "% of dst": geometric coverage. avg is the per-compaction mean of
		// (fan-in / dst-total), not the bytes-weighted version, since the
		// destination total varies across compactions as the level grows.
		fmt.Printf("    %-16s                  pct[fan-in/dst]=%.2f%%(avg) min=%.2f%% max=%.2f%% (n=%d)\n",
			"", b.AvgPctOfOutput()*100, b.MinPct*100, b.MaxPct*100, b.PctCount)
	}
}

// writeCompactionMarkdown emits the per-kind compaction breakdown as rows in
// the single-result markdown table. Only kinds that actually fired produce
// rows so empty configurations don't clutter the output. Each kind with a
// meaningful fan-in (L0→Lbase, Lbase+) gets two rows: the WA ratio
// (fan-in/source) and the geometric pct (fan-in/destination-total).
func writeCompactionMarkdown(b *strings.Builder, s CompactionStats) {
	for _, k := range []struct {
		label  string
		bucket CompactionBucket
		ratio  bool
	}{
		{"L0->Lbase", s.L0Lbase, true},
		{"Lbase+", s.LbasePlus, true},
		{"intra-L0", s.IntraL0, false},
	} {
		if k.bucket.Count == 0 {
			continue
		}
		source := k.bucket.L0Bytes
		if k.bucket.StartBytes > 0 {
			source = k.bucket.StartBytes
		}
		if k.ratio {
			b.WriteString(fmt.Sprintf("| %s (n / src / fan-in / WA ratio) | %d / %s / %s / %.2f |\n",
				k.label, k.bucket.Count, FormatSize(source), FormatSize(k.bucket.FanInBytes),
				weightedFanInRatio(k.bucket)))
			if k.bucket.PctCount > 0 {
				b.WriteString(fmt.Sprintf("| %s (pct of dst avg / min / max) | %.2f%% / %.2f%% / %.2f%% |\n",
					k.label, k.bucket.AvgPctOfOutput()*100, k.bucket.MinPct*100, k.bucket.MaxPct*100))
			}
		} else {
			b.WriteString(fmt.Sprintf("| %s (n / bytes) | %d / %s |\n",
				k.label, k.bucket.Count, FormatSize(source)))
		}
	}
}

// fmtDur formats a duration with a resolution appropriate to its magnitude,
// since sync calls are often well under a millisecond.
func fmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d >= time.Second:
		return d.Round(time.Millisecond).String()
	case d >= time.Millisecond:
		return d.Round(10 * time.Microsecond).String()
	default:
		return d.Round(time.Microsecond).String()
	}
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
