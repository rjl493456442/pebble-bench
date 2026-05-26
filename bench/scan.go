package bench

import (
	"context"
	"time"

	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/datagen"
	"github.com/rjl493456442/pebble-bench/db"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// Scan iterates over keys using a Pebble iterator, measuring per-key
// Next throughput. Each worker starts from the beginning of the keyspace
// and wraps around when reaching the end.

type Scan struct {
	db db.DB
}

func (s *Scan) Name() string { return "scan" }

func (s *Scan) Setup(database db.DB, _ bool, _ *config.BenchmarkConfig, _ *datagen.Meta) error {
	s.db = database
	return nil
}

func (s *Scan) Run(ctx context.Context, _ int, hist *metrics.NamedHistogram) error {
	iter, err := s.db.NewIter()
	if err != nil {
		return err
	}
	defer iter.Close()

	iter.First()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		start := time.Now()
		if !iter.Next() {
			// Wrap around to the beginning.
			iter.First()
			if !iter.Valid() {
				return iter.Error()
			}
		}
		elapsed := time.Since(start)

		hist.Record(elapsed)
		if !IncrementOps(ctx) {
			return nil
		}
	}
}
