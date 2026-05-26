package db

import (
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/rjl493456442/pebble-bench/config"
)

func TestBuildV1LevelOptionsDefaults(t *testing.T) {
	levels := buildV1LevelOptions(nil, 10)
	if len(levels) != len(defaultLevelTargetSizes) {
		t.Fatalf("got %d levels, want %d", len(levels), len(defaultLevelTargetSizes))
	}
	for i := range levels {
		if levels[i].TargetFileSize != defaultLevelTargetSizes[i] {
			t.Errorf("L%d target file size = %d, want %d", i, levels[i].TargetFileSize, defaultLevelTargetSizes[i])
		}
		// Bloom filter on every level except the last.
		wantFilter := i < len(levels)-1
		if got := levels[i].FilterPolicy != nil; got != wantFilter {
			t.Errorf("L%d filter present = %v, want %v", i, got, wantFilter)
		}
	}
}

func TestBuildV1LevelOptionsOverlay(t *testing.T) {
	bits := 0
	levels := []config.LevelConfig{
		{Compression: "none", BlockSize: 8192}, // L0: tweak some fields, keep default size
		{},                                     // L1: fully inherit defaults
		{TargetFileSize: 1 << 30, NoFilter: true},
		{BloomFilterBits: &bits}, // L3: zero bits disables the filter
	}
	opts := buildV1LevelOptions(levels, 10)

	// Number of levels stays at the default count since overrides are fewer.
	if len(opts) != len(defaultLevelTargetSizes) {
		t.Fatalf("got %d levels, want %d", len(opts), len(defaultLevelTargetSizes))
	}

	// L0: compression and block size overridden, target size inherited.
	if opts[0].Compression != pebble.NoCompression {
		t.Errorf("L0 compression = %v, want NoCompression", opts[0].Compression)
	}
	if opts[0].BlockSize != 8192 {
		t.Errorf("L0 block size = %d, want 8192", opts[0].BlockSize)
	}
	if opts[0].TargetFileSize != defaultLevelTargetSizes[0] {
		t.Errorf("L0 target size = %d, want inherited %d", opts[0].TargetFileSize, defaultLevelTargetSizes[0])
	}

	// L1: untouched defaults.
	if opts[1].TargetFileSize != defaultLevelTargetSizes[1] || opts[1].FilterPolicy == nil {
		t.Errorf("L1 not at defaults: size=%d filter=%v", opts[1].TargetFileSize, opts[1].FilterPolicy != nil)
	}

	// L2: explicit size + filter disabled.
	if opts[2].TargetFileSize != 1<<30 {
		t.Errorf("L2 target size = %d, want %d", opts[2].TargetFileSize, 1<<30)
	}
	if opts[2].FilterPolicy != nil {
		t.Errorf("L2 filter should be disabled via no_filter")
	}

	// L3: zero bloom bits disables the filter.
	if opts[3].FilterPolicy != nil {
		t.Errorf("L3 filter should be disabled via zero bloom_filter_bits")
	}
}

func TestBuildV1LevelOptionsExtraLevels(t *testing.T) {
	// Provide more levels than the defaults.
	levels := make([]config.LevelConfig, len(defaultLevelTargetSizes)+2)
	opts := buildV1LevelOptions(levels, 10)
	if len(opts) != len(levels) {
		t.Fatalf("got %d levels, want %d", len(opts), len(levels))
	}
	// Extra levels inherit the largest default target size.
	last := defaultLevelTargetSizes[len(defaultLevelTargetSizes)-1]
	for i := len(defaultLevelTargetSizes); i < len(opts); i++ {
		if opts[i].TargetFileSize != last {
			t.Errorf("L%d target size = %d, want inherited %d", i, opts[i].TargetFileSize, last)
		}
	}
}

func TestV1Compression(t *testing.T) {
	cases := map[string]struct {
		want pebble.Compression
		ok   bool
	}{
		"":              {pebble.DefaultCompression, true},
		"default":       {pebble.DefaultCompression, true},
		"none":          {pebble.NoCompression, true},
		"NoCompression": {pebble.NoCompression, true},
		"snappy":        {pebble.SnappyCompression, true},
		"ZSTD":          {pebble.ZstdCompression, true},
		"bogus":         {pebble.DefaultCompression, false},
	}
	for in, want := range cases {
		got, ok := v1Compression(in)
		if got != want.want || ok != want.ok {
			t.Errorf("v1Compression(%q) = (%v, %v), want (%v, %v)", in, got, ok, want.want, want.ok)
		}
	}
}
