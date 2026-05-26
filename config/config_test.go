package config

import "testing"

func TestValidCompression(t *testing.T) {
	valid := []string{"", "default", "none", "no", "NoCompression", "snappy", "ZSTD"}
	for _, s := range valid {
		if !validCompression(s) {
			t.Errorf("validCompression(%q) = false, want true", s)
		}
	}
	if validCompression("bogus") {
		t.Errorf("validCompression(%q) = true, want false", "bogus")
	}
}

func TestValidateRejectsBadCompression(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Levels = []LevelConfig{{Compression: "bogus"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for invalid compression")
	}
}
