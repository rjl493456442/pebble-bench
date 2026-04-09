package datagen

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/cockroachdb/pebble"
)

// Populate fills the database to approximately the target size.
func Populate(db *pebble.DB, targetBytes int64, keySize, valueSize, batchSize int, writeOpts *pebble.WriteOptions) (*Meta, error) {
	// Check current size
	m := db.Metrics()
	currentSize := int64(m.DiskSpaceUsage())
	if currentSize >= targetBytes {
		log.Printf("Database already at %s (target %s), skipping population",
			formatBytes(currentSize), formatBytes(targetBytes))

		// Try to load existing meta, or estimate from current state
		totalKeys := uint64(currentSize) / uint64(keySize+valueSize)
		return &Meta{TotalKeys: totalKeys, KeySize: keySize, ValueSize: valueSize}, nil
	}

	log.Printf("Populating database to %s (current: %s)", formatBytes(targetBytes), formatBytes(currentSize))

	var (
		rng       = rand.New(rand.NewSource(42))
		totalKeys uint64
		startTime = time.Now()
		lastLog   = startTime
	)
	for {
		batch := db.NewBatch()
		for i := 0; i < batchSize; i++ {
			key := KeyForIndex(totalKeys)
			val := RandomValue(rng, valueSize)

			if err := batch.Set(key, val, nil); err != nil {
				batch.Close()
				return nil, fmt.Errorf("batch set: %w", err)
			}
			totalKeys++
		}
		if err := batch.Commit(writeOpts); err != nil {
			batch.Close()
			return nil, fmt.Errorf("batch commit: %w", err)
		}
		batch.Close()

		// Periodic progress check
		if time.Since(lastLog) > 10*time.Second {
			m = db.Metrics()
			currentSize = int64(m.DiskSpaceUsage())
			elapsed := time.Since(startTime)
			keysPerSec := float64(totalKeys) / elapsed.Seconds()

			log.Printf("Progress: %s / %s (%.1f%%), %d keys, %.0f keys/sec",
				formatBytes(currentSize), formatBytes(targetBytes),
				float64(currentSize)/float64(targetBytes)*100,
				totalKeys, keysPerSec)

			if currentSize >= targetBytes {
				break
			}
			lastLog = time.Now()
		}

		// Fast estimation check every 10K batches
		if totalKeys%(uint64(batchSize)*10) == 0 {
			m = db.Metrics()
			if int64(m.DiskSpaceUsage()) >= targetBytes {
				break
			}
		}
	}

	// Flush to ensure all data is on disk
	if err := db.Flush(); err != nil {
		return nil, fmt.Errorf("flushing database: %w", err)
	}

	m = db.Metrics()
	finalSize := int64(m.DiskSpaceUsage())
	elapsed := time.Since(startTime)

	log.Printf("Population complete: %s, %d keys, took %s",
		formatBytes(finalSize), totalKeys, elapsed.Round(time.Second))

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
