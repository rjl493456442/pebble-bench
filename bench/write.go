package bench

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/datagen"
	"github.com/rjl493456442/pebble-bench/db"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// Write benchmarks batched key-value writes under one of two key layouts;
// see config.BenchmarkConfig.KeyPattern for why both exist.
type Write struct {
	db          db.DB
	sync        bool
	batchSize   int
	valueSize   int
	concurrency int
	streams     int // 0 for random keys, else the number of sorted streams
}

func (b *Write) Name() string { return "write" }

func (b *Write) Setup(database db.DB, sync bool, cfg *config.BenchmarkConfig, _ *datagen.Meta) error {
	b.db = database
	b.sync = sync
	b.batchSize = cfg.BatchSize
	b.valueSize = cfg.ValueSize
	b.concurrency = max(cfg.Concurrency, 1)
	if b.batchSize < 1 {
		b.batchSize = 100
	}
	streams, err := parseKeyPattern(cfg.KeyPattern)
	if err != nil {
		return err
	}
	b.streams = streams
	if streams == 0 {
		log.Printf("Write key pattern: random")
	} else {
		log.Printf("Write key pattern: sorted, %d append-only streams over disjoint ranges (%d per worker)",
			streams, streams/b.concurrency)
		if streams%b.concurrency != 0 {
			log.Printf("WARNING: %d streams do not divide evenly over %d workers; some workers carry one more than others", streams, b.concurrency)
		}
	}
	return nil
}

// parseKeyPattern returns the number of sorted streams, or 0 for random keys.
func parseKeyPattern(p string) (int, error) {
	p = strings.ToLower(strings.TrimSpace(p))
	switch {
	case p == "" || p == "random":
		return 0, nil
	case p == "sorted":
		return 16, nil
	case strings.HasPrefix(p, "sorted:"):
		n, err := strconv.Atoi(strings.TrimPrefix(p, "sorted:"))
		if err != nil || n < 1 || n > 65536 {
			return 0, fmt.Errorf("key_pattern %q: want sorted:N with 1 <= N <= 65536", p)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unknown key_pattern %q (random, sorted, sorted:N)", p)
	}
}

// sortedKey is the n-th key of stream r out of streams. The first two bytes
// place the stream in its own slice of the key space, the next eight order the
// keys within it, and the rest are a hash so the keys look like the digests
// they stand in for. Within a stream keys are strictly increasing in n; across
// streams they never interleave.
func sortedKey(r, streams int, n uint64) []byte {
	k := make([]byte, 32)
	binary.BigEndian.PutUint16(k[0:2], uint16(uint64(r)*65536/uint64(streams)))
	binary.BigEndian.PutUint64(k[2:10], n)
	h := sha256.Sum256(k[:10])
	copy(k[10:], h[:22])
	return k
}

func (b *Write) Run(ctx context.Context, workerID int, reg *metrics.HistogramRegistry) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
	hist := reg.Get("write")

	// Streams owned by this worker, and each one's next key. Workers take
	// streams round-robin so that every stream has exactly one writer, which
	// is what keeps it strictly increasing.
	var owned []int
	for r := workerID; r < b.streams; r += b.concurrency {
		owned = append(owned, r)
	}
	if b.streams > 0 && len(owned) == 0 {
		log.Printf("worker %d: no stream to write (fewer streams than workers), idle", workerID)
		<-ctx.Done()
		return nil
	}
	next := make([]uint64, len(owned))
	turn := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		batch := b.db.NewBatch()
		if b.streams == 0 {
			for range b.batchSize {
				key := datagen.RandomValue(rng, 32)
				val := datagen.RandomValue(rng, b.valueSize)
				if err := batch.Set(key, val); err != nil {
					batch.Close()
					return err
				}
			}
		} else {
			// One batch is one contiguous run of keys at the frontier of one
			// stream, the way a range fetcher commits what it just received.
			i := turn % len(owned)
			turn++
			for range b.batchSize {
				key := sortedKey(owned[i], b.streams, next[i])
				next[i]++
				val := datagen.RandomValue(rng, b.valueSize)
				if err := batch.Set(key, val); err != nil {
					batch.Close()
					return err
				}
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
