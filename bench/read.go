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

// Read reads keys in random order.
type Read struct {
	db        *pebble.DB
	totalKeys uint64
}

func (r *Read) Name() string { return "read" }

func (r *Read) Setup(db *pebble.DB, _ *pebble.WriteOptions, _ *config.BenchmarkConfig, meta *datagen.Meta) error {
	r.db = db
	r.totalKeys = meta.TotalKeys
	return nil
}

func (r *Read) Run(ctx context.Context, workerID int, hist *metrics.NamedHistogram) error {
	rng := rand.New(rand.NewSource(int64(workerID) * 31337))

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

		if err == pebble.ErrNotFound {
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
