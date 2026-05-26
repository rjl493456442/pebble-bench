package bench

import (
	"context"
	"math/rand"
	"time"

	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/datagen"
	"github.com/rjl493456442/pebble-bench/db"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// Mixed performs a mix of reads and batch writes based on a configurable ratio.
type Mixed struct {
	db          db.DB
	sync        bool
	totalKeys   uint64
	readPercent int
	batchSize   int
	valueSize   int
}

func (m *Mixed) Name() string { return "mixed" }

func (m *Mixed) Setup(database db.DB, sync bool, cfg *config.BenchmarkConfig, meta *datagen.Meta) error {
	m.db = database
	m.sync = sync
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

func (m *Mixed) Run(ctx context.Context, workerID int, reg *metrics.HistogramRegistry) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
	readHist := reg.Get("read")
	writeHist := reg.Get("write")

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

			if err == db.ErrNotFound {
				continue
			}
			if err != nil {
				return err
			}
			closer.Close()
			readHist.Record(elapsed)
		} else {
			batch := m.db.NewBatch()
			for range m.batchSize {
				key := datagen.RandomValue(rng, 32)
				val := datagen.RandomValue(rng, m.valueSize)
				if err := batch.Set(key, val); err != nil {
					batch.Close()
					return err
				}
			}

			start := time.Now()
			err := batch.Commit(m.sync)
			elapsed := time.Since(start)
			batch.Close()

			if err != nil {
				return err
			}
			writeHist.Record(elapsed)
		}

		if !IncrementOps(ctx) {
			return nil
		}
	}
}
