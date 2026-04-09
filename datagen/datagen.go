package datagen

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
)

// Meta stores metadata about the populated dataset.
type Meta struct {
	TotalKeys uint64 `json:"total_keys"`
	KeySize   int    `json:"key_size"`
	ValueSize int    `json:"value_size"`
}

// SaveMeta writes metadata to a JSON file in the data directory.
func SaveMeta(dir string, meta *Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "bench_meta.json"), data, 0644)
}

// LoadMeta reads metadata from the data directory.
func LoadMeta(dir string) (*Meta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "bench_meta.json"))
	if err != nil {
		return nil, fmt.Errorf("loading metadata (have you run 'init' first?): %w", err)
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// KeyForIndex generates a deterministic key for the given index.
// The key is sha256(bigEndian(index)), producing uniformly distributed
// 32-byte keys similar to Ethereum state trie keys.
func KeyForIndex(index uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], index)
	h := sha256.Sum256(buf[:])
	return h[:]
}

// RandomValue generates a random value of the specified size.
func RandomValue(rng *rand.Rand, size int) []byte {
	val := make([]byte, size)
	for i := 0; i < size; i += 8 {
		v := rng.Int63()
		remaining := size - i
		if remaining >= 8 {
			binary.LittleEndian.PutUint64(val[i:], uint64(v))
		} else {
			for j := 0; j < remaining; j++ {
				val[i+j] = byte(v >> (j * 8))
			}
		}
	}
	return val
}
