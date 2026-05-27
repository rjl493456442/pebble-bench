package db

import (
	"fmt"
	"testing"

	"github.com/rjl493456442/pebble-bench/config"
	"github.com/rjl493456442/pebble-bench/metrics"
)

func openInstrumented(t *testing.T, cfg *config.BenchConfig, sync *metrics.SyncTracker, read *metrics.ReadTracker) (DB, func()) {
	t.Helper()
	database, _, cleanup, err := Open(cfg, metrics.NewFlushTracker(), metrics.NewWriteStallTracker(), sync, read)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return database, cleanup
}

// testVFSInstrumentation verifies that the instrumented VFS records both the
// durability and read syscalls Pebble issues:
//   - synchronous commits force a WAL data-sync per commit (fdatasync), and
//     flushing/closing forces full Syncs (fsync) on the manifest and sstables;
//   - reopening with a cold cache and reading the keys back forces sstable
//     block reads (pread) at the VFS boundary.
func testVFSInstrumentation(t *testing.T, v2 bool) {
	cfg := &config.BenchConfig{
		DataDir:  t.TempDir(),
		CacheMB:  64,
		Handles:  500,
		PebbleV2: v2,
	}
	syncTracker := metrics.NewSyncTracker()
	readTracker := metrics.NewReadTracker()

	// Phase 1: write synchronously, flush to an sstable, then close.
	database, cleanup := openInstrumented(t, cfg, syncTracker, readTracker)
	const keys = 200
	for i := range keys {
		b := database.NewBatch()
		if err := b.Set(fmt.Appendf(nil, "key-%08d", i), make([]byte, 256)); err != nil {
			t.Fatal(err)
		}
		if err := b.Commit(true); err != nil {
			t.Fatal(err)
		}
		b.Close()
	}
	if err := database.Flush(); err != nil {
		t.Fatal(err)
	}
	cleanup()

	s := syncTracker.Stats()
	if s.SyncData.Count == 0 {
		t.Errorf("expected fdatasync (SyncData) calls from synchronous commits, got 0")
	}
	if s.Sync.Count == 0 {
		t.Errorf("expected fsync (Sync) calls from flushing/closing, got 0")
	}

	// Phase 2: reopen with a cold block cache and read the data back, which must
	// hit the disk through vfs.File.ReadAt (pread).
	database, cleanup = openInstrumented(t, cfg, syncTracker, readTracker)
	iter, err := database.NewIter()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for iter.First(); iter.Valid(); iter.Next() {
		count++
	}
	if err := iter.Error(); err != nil {
		t.Fatal(err)
	}
	iter.Close()
	cleanup()

	r := readTracker.Stats()
	t.Logf("v2=%v fsync=%d(avg %s) fdatasync=%d(avg %s) | pread=%d(avg %s) read=%d(avg %s) | scanned=%d",
		v2, s.Sync.Count, s.Sync.AvgTime(), s.SyncData.Count, s.SyncData.AvgTime(),
		r.ReadAt.Count, r.ReadAt.AvgTime(), r.Read.Count, r.Read.AvgTime(), count)

	if count < keys {
		t.Errorf("expected to scan at least %d keys, got %d", keys, count)
	}
	if r.ReadAt.Count == 0 {
		t.Errorf("expected pread (ReadAt) calls from reading sstables, got 0")
	}
}

func TestVFSInstrumentationV1(t *testing.T) { testVFSInstrumentation(t, false) }
func TestVFSInstrumentationV2(t *testing.T) { testVFSInstrumentation(t, true) }
