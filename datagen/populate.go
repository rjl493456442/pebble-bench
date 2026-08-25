package datagen

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"

	"github.com/rjl493456442/pebble-bench/db"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// progressPoint records a snapshot during population.
type progressPoint struct {
	elapsed      time.Duration
	keys         uint64
	size         int64
	intervalRate float64 // keys/sec for this interval
	overallRate  float64 // keys/sec overall
}

// Populate fills the database to approximately the target size.
// If existing is non-nil, population resumes from the existing key count.
func Populate(database db.DB, targetBytes int64, keySize, valueSize, batchSize int, sync bool, profile string, existing *Meta) (*Meta, error) {
	// Progress and the stop condition are driven by the logical volume of data
	// written (keys * entry size), not by Pebble's DiskSpaceUsage. The latter
	// includes the WAL, obsolete and zombie files that are deleted asynchronously
	// after compactions, so it is both non-monotonic and inflated by transient
	// garbage. Using it would (a) make progress bounce up and down and (b) let a
	// write-heavy run (more compaction churn -> more zombie files) trip the target
	// early with less real data than a low-churn baseline, making the two datasets
	// unequal and the later read/scan comparison unfair.
	entryBytes := int64(keySize + valueSize)

	// TotalKeys from the saved metadata is the authoritative count of existing
	// data; DiskSpaceUsage is only reported for context.
	var startIndex uint64
	if existing != nil {
		startIndex = existing.TotalKeys
	}
	writtenBytes := int64(startIndex) * entryBytes

	m := database.Metrics()
	physicalSize := int64(m.DiskSpaceUsage)
	if writtenBytes >= targetBytes {
		log.Printf("Database already at %s logical (%s on disk, target %s), skipping population",
			formatBytes(writtenBytes), formatBytes(physicalSize), formatBytes(targetBytes))
		if existing != nil {
			return existing, nil
		}
		return &Meta{TotalKeys: startIndex, KeySize: keySize, ValueSize: valueSize}, nil
	}

	if existing != nil {
		log.Printf("Extending dataset from %d keys (%s logical, %s on disk) to target %s",
			startIndex, formatBytes(writtenBytes), formatBytes(physicalSize), formatBytes(targetBytes))
	} else {
		log.Printf("Populating database to %s (current: %s on disk)", formatBytes(targetBytes), formatBytes(physicalSize))
	}

	var (
		rng         = rand.New(rand.NewSource(int64(startIndex)))
		totalKeys   = startIndex
		newKeys     uint64
		startTime   = time.Now()
		lastLog     = startTime
		lastLogKeys uint64
		points      []progressPoint
	)
	for {
		batch := database.NewBatch()
		for i := 0; i < batchSize; i++ {
			key := KeyForIndex(totalKeys)
			val := RandomValue(rng, valueSize)

			if err := batch.Set(key, val); err != nil {
				batch.Close()
				return nil, fmt.Errorf("batch set: %w", err)
			}
			totalKeys++
			newKeys++
		}
		if err := batch.Commit(sync); err != nil {
			batch.Close()
			return nil, fmt.Errorf("batch commit: %w", err)
		}
		batch.Close()

		writtenBytes = int64(totalKeys) * entryBytes

		// Periodic progress check
		if time.Since(lastLog) > 10*time.Second {
			now := time.Now()
			m = database.Metrics()
			physicalSize = int64(m.DiskSpaceUsage)

			intervalKeys := newKeys - lastLogKeys
			intervalSec := now.Sub(lastLog).Seconds()
			intervalRate := float64(intervalKeys) / intervalSec
			overallRate := float64(newKeys) / now.Sub(startTime).Seconds()

			points = append(points, progressPoint{
				elapsed:      now.Sub(startTime),
				keys:         totalKeys,
				size:         physicalSize,
				intervalRate: intervalRate,
				overallRate:  overallRate,
			})

			// Progress and % track logical bytes (monotonic); the on-disk figure
			// is shown alongside for space-amplification context.
			log.Printf("Progress: %s / %s (%.1f%%), %d keys, disk %s, interval %.0f keys/sec, overall %.0f keys/sec",
				formatBytes(writtenBytes), formatBytes(targetBytes),
				float64(writtenBytes)/float64(targetBytes)*100,
				totalKeys, formatBytes(physicalSize), intervalRate, overallRate)

			lastLog = now
			lastLogKeys = newKeys
		}

		if writtenBytes >= targetBytes {
			break
		}
	}

	// Flush to ensure all data is on disk
	if err := database.Flush(); err != nil {
		return nil, fmt.Errorf("flushing database: %w", err)
	}

	m = database.Metrics()
	finalPhysical := int64(m.DiskSpaceUsage)
	finalLogical := int64(totalKeys) * entryBytes
	elapsed := time.Since(startTime)
	overallRate := float64(newKeys) / elapsed.Seconds()

	// Print final summary
	fmt.Println()
	fmt.Println("========== Population Summary ==========")
	fmt.Printf("  Profile:         %s\n", profile)
	fmt.Printf("  New Keys:        %d\n", newKeys)
	fmt.Printf("  Total Keys:      %d\n", totalKeys)
	fmt.Printf("  Logical Size:    %s\n", formatBytes(finalLogical))
	fmt.Printf("  On-Disk Size:    %s\n", formatBytes(finalPhysical))
	fmt.Printf("  Duration:        %s\n", elapsed.Round(time.Second))
	fmt.Printf("  Overall Speed:   %.0f keys/sec\n", overallRate)
	// The raw SST bytes are BytesWritten minus the WAL bytes Pebble folds into its
	// flushed total, and the honest write amp normalises those by the logical WAL
	// ingest rather than by the recycling-inflated physical WAL byte count.
	tableBytesRaw := int64(m.BytesWritten) - int64(m.BytesIn)
	if tableBytesRaw < 0 {
		tableBytesRaw = 0
	}
	var writeAmpLogical float64
	if m.WALBytesIn > 0 {
		writeAmpLogical = float64(tableBytesRaw) / float64(m.WALBytesIn)
	}
	fmt.Printf("  Write Amp (logical): %.2f  <- cross-comparable\n", writeAmpLogical)
	fmt.Printf("  Write Amp (Pebble):  %.2f  (denominator = physical WAL, not comparable)\n", m.WriteAmp)
	fmt.Printf("  SST Bytes Written:   %s (flush+compaction)\n", formatBytes(tableBytesRaw))
	fmt.Printf("  Bytes Read:          %s (compaction)\n", formatBytes(int64(m.BytesRead)))
	fmt.Printf("  WAL Bytes In:        %s (logical ingest)\n", formatBytes(int64(m.WALBytesIn)))
	fmt.Printf("  WAL Bytes Written:   %s (physical, recycling-inflated)\n", formatBytes(int64(m.WALBytesWritten)))
	if len(points) > 0 {
		var minRate, maxRate float64
		minRate = math.MaxFloat64
		for _, p := range points {
			if p.intervalRate < minRate {
				minRate = p.intervalRate
			}
			if p.intervalRate > maxRate {
				maxRate = p.intervalRate
			}
		}
		fmt.Printf("  Min Speed:       %.0f keys/sec\n", minRate)
		fmt.Printf("  Max Speed:       %.0f keys/sec\n", maxRate)
	}
	fmt.Println("=========================================")

	// Print write speed chart
	if len(points) >= 2 {
		chartPoints := make([]metrics.ChartPoint, len(points))
		for i, p := range points {
			chartPoints[i] = metrics.ChartPoint{
				Elapsed: p.elapsed,
				Value:   p.intervalRate,
			}
		}
		metrics.PrintChart("Write Speed Over Time (keys/sec)", chartPoints)
	}

	return &Meta{
		TotalKeys: totalKeys,
		KeySize:   keySize,
		ValueSize: valueSize,
		Populate: &PopulateStats{
			Profile:      profile,
			NewKeys:      newKeys,
			DurationSec:  elapsed.Seconds(),
			OverallRate:  overallRate,
			WriteAmp:        m.WriteAmp,
			WriteAmpLogical: writeAmpLogical,
			BytesIn:         m.BytesIn,
			BytesWritten:    m.BytesWritten,
			TableBytesRaw:   uint64(tableBytesRaw),
			BytesRead:       m.BytesRead,
			WALBytesIn:      m.WALBytesIn,
			WALBytesWritten: m.WALBytesWritten,
			LogicalBytes:    finalLogical,
			OnDiskBytes:  finalPhysical,
			LevelSizes:   m.LevelSizes,
			LevelFiles:   m.LevelFiles,
		},
	}, nil
}

func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.2fGB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.2fMB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.2fKB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%dB", b)
	}
}
