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
func Populate(database db.DB, targetBytes int64, keySize, valueSize, batchSize int, sync bool, existing *Meta) (*Meta, error) {
	// Check current size
	m := database.Metrics()
	currentSize := int64(m.DiskSpaceUsage)
	if currentSize >= targetBytes {
		log.Printf("Database already at %s (target %s), skipping population",
			formatBytes(currentSize), formatBytes(targetBytes))
		if existing != nil {
			return existing, nil
		}
		totalKeys := uint64(currentSize) / uint64(keySize+valueSize)
		return &Meta{TotalKeys: totalKeys, KeySize: keySize, ValueSize: valueSize}, nil
	}

	// Resume from existing key count
	var startIndex uint64
	if existing != nil {
		startIndex = existing.TotalKeys
		log.Printf("Extending dataset from %d keys (%s) to target %s",
			startIndex, formatBytes(currentSize), formatBytes(targetBytes))
	} else {
		log.Printf("Populating database to %s (current: %s)", formatBytes(targetBytes), formatBytes(currentSize))
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

		// Periodic progress check
		if time.Since(lastLog) > 10*time.Second {
			now := time.Now()
			m = database.Metrics()
			currentSize = int64(m.DiskSpaceUsage)

			intervalKeys := newKeys - lastLogKeys
			intervalSec := now.Sub(lastLog).Seconds()
			intervalRate := float64(intervalKeys) / intervalSec
			overallRate := float64(newKeys) / now.Sub(startTime).Seconds()

			points = append(points, progressPoint{
				elapsed:      now.Sub(startTime),
				keys:         totalKeys,
				size:         currentSize,
				intervalRate: intervalRate,
				overallRate:  overallRate,
			})

			log.Printf("Progress: %s / %s (%.1f%%), %d keys, interval %.0f keys/sec, overall %.0f keys/sec",
				formatBytes(currentSize), formatBytes(targetBytes),
				float64(currentSize)/float64(targetBytes)*100,
				totalKeys, intervalRate, overallRate)

			if currentSize >= targetBytes {
				break
			}
			lastLog = now
			lastLogKeys = newKeys
		}

		// Fast estimation check every 10K batches
		if totalKeys%(uint64(batchSize)*10) == 0 {
			m = database.Metrics()
			if int64(m.DiskSpaceUsage) >= targetBytes {
				break
			}
		}
	}

	// Flush to ensure all data is on disk
	if err := database.Flush(); err != nil {
		return nil, fmt.Errorf("flushing database: %w", err)
	}

	m = database.Metrics()
	finalSize := int64(m.DiskSpaceUsage)
	elapsed := time.Since(startTime)
	overallRate := float64(newKeys) / elapsed.Seconds()

	// Print final summary
	fmt.Println()
	fmt.Println("========== Population Summary ==========")
	fmt.Printf("  New Keys:        %d\n", newKeys)
	fmt.Printf("  Total Keys:      %d\n", totalKeys)
	fmt.Printf("  Final Size:      %s\n", formatBytes(finalSize))
	fmt.Printf("  Duration:        %s\n", elapsed.Round(time.Second))
	fmt.Printf("  Overall Speed:   %.0f keys/sec\n", overallRate)
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
