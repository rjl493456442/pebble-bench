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

// Read reads keys in random order.
type Read struct {
	db        db.DB
	totalKeys uint64
}

func (r *Read) Name() string { return "read" }

func (r *Read) Setup(database db.DB, _ bool, _ *config.BenchmarkConfig, meta *datagen.Meta) error {
	r.db = database
	r.totalKeys = meta.TotalKeys
	return nil
}

func (r *Read) Run(ctx context.Context, workerID int, reg *metrics.HistogramRegistry) error {
	rng := rand.New(rand.NewSource(int64(workerID) * 31337))
	hist := reg.Get("read")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		index := rng.Uint64() % r.totalKeys
		key := datagen.KeyForIndex(index)

		start := time.Now()
		_, closer, err := r.db.Get(key)
		elapsed := time.Since(start)

		if err == db.ErrNotFound {
			continue
		}
		if err != nil {
			return err
		}
		closer.Close()

		hist.Record(elapsed)
		if !IncrementOps(ctx) {
			return nil
		}
	}
}
