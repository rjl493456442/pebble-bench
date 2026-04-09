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

// Write performs batched key-value writes.
type Write struct {
	db        *pebble.DB
	writeOpts *pebble.WriteOptions
	batchSize int
	valueSize int
}

func (b *Write) Name() string { return "write" }

func (b *Write) Setup(db *pebble.DB, writeOpts *pebble.WriteOptions, cfg *config.BenchmarkConfig, _ *datagen.Meta) error {
	b.db = db
	b.writeOpts = writeOpts
	b.batchSize = cfg.BatchSize
	b.valueSize = cfg.ValueSize
	if b.batchSize < 1 {
		b.batchSize = 100
	}
	return nil
}

func (b *Write) Run(ctx context.Context, workerID int, hist *metrics.NamedHistogram) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		batch := b.db.NewBatch()
		for i := range b.batchSize {
			_ = i
			key := datagen.RandomValue(rng, 32)
			val := datagen.RandomValue(rng, b.valueSize)
			if err := batch.Set(key, val, nil); err != nil {
				batch.Close()
				return err
			}
		}

		start := time.Now()
		err := batch.Commit(b.writeOpts)
		elapsed := time.Since(start)
		batch.Close()

		if err != nil {
			return err
		}

		hist.Record(elapsed)
		if !IncrementOps(ctx) {
			return nil
		}
	}
}
