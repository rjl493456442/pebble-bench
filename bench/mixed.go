package bench

import (
	"context"
	"math/rand"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/datagen"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// Mixed performs a mix of reads and batch writes based on a configurable ratio.
type Mixed struct {
	db          *pebble.DB
	writeOpts   *pebble.WriteOptions
	totalKeys   uint64
	readPercent int
	batchSize   int
	valueSize   int
}

func (m *Mixed) Name() string { return "mixed" }

func (m *Mixed) Setup(db *pebble.DB, writeOpts *pebble.WriteOptions, cfg *config.BenchmarkConfig, meta *datagen.Meta) error {
	m.db = db
	m.writeOpts = writeOpts
	m.totalKeys = meta.TotalKeys
	m.readPercent = cfg.ReadPercent
	m.batchSize = cfg.BatchSize
	m.valueSize = cfg.ValueSize
	if m.readPercent <= 0 || m.readPercent >= 100 {
		m.readPercent = 80
	}
	if m.batchSize < 1 {
		m.batchSize = 100
	}
	return nil
}

func (m *Mixed) Run(ctx context.Context, workerID int, hist *metrics.NamedHistogram) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		isRead := rng.Intn(100) < m.readPercent

		if isRead {
			index := rng.Uint64() % m.totalKeys
			key := datagen.KeyForIndex(index)

			start := time.Now()
			_, closer, err := m.db.Get(key)
			elapsed := time.Since(start)

			if err == pebble.ErrNotFound {
				continue
			}
			if err != nil {
				return err
			}
			closer.Close()
			hist.Record(elapsed)
		} else {
			batch := m.db.NewBatch()
			for range m.batchSize {
				key := datagen.RandomValue(rng, 32)
				val := datagen.RandomValue(rng, m.valueSize)
				if err := batch.Set(key, val, nil); err != nil {
					batch.Close()
					return err
				}
			}

			start := time.Now()
			err := batch.Commit(m.writeOpts)
			elapsed := time.Since(start)
			batch.Close()

			if err != nil {
				return err
			}
			hist.Record(elapsed)
		}

		if !IncrementOps(ctx) {
			return nil
		}
	}
}
