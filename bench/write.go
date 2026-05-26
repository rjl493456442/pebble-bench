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

// Write performs batched key-value writes.
type Write struct {
	db        db.DB
	sync      bool
	batchSize int
	valueSize int
}

func (b *Write) Name() string { return "write" }

func (b *Write) Setup(database db.DB, sync bool, cfg *config.BenchmarkConfig, _ *datagen.Meta) error {
	b.db = database
	b.sync = sync
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
			if err := batch.Set(key, val); err != nil {
				batch.Close()
				return err
			}
		}

		start := time.Now()
		err := batch.Commit(b.sync)
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
