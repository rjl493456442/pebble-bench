package metrics

// RunConfig is what gets recorded in a Result's Config field. It captures the
// fully-resolved database configuration (with all Pebble defaults materialized)
// alongside the benchmark parameters, so a result file is unambiguous about what
// was actually run — no nil/zero "means default" fields.
type RunConfig struct {
	Pebble    *ResolvedConfig `json:"pebble"`
	Benchmark interface{}     `json:"benchmark"`
}

// ResolvedConfig is the effective Pebble configuration used for a run, read back
// from the options after Pebble applied its defaults.
type ResolvedConfig struct {
	PebbleVersion               string          `json:"pebble_version"`
	DataDir                     string          `json:"data_dir"`
	CacheMB                     int             `json:"cache_mb"`
	MaxOpenFiles                int             `json:"max_open_files"`
	ReadOnly                    bool            `json:"read_only"`
	NoSync                      bool            `json:"no_sync"`
	DisableWAL                  bool            `json:"disable_wal"`
	MemTableSize                uint64          `json:"mem_table_size"`
	MemTableStopWritesThreshold int             `json:"mem_table_stop_writes_threshold"`
	MaxConcurrentCompactions    int             `json:"max_concurrent_compactions"`
	L0CompactionThreshold       int             `json:"l0_compaction_threshold"`
	L0StopWritesThreshold       int             `json:"l0_stop_writes_threshold"`
	L0CompactionConcurrency     int             `json:"l0_compaction_concurrency"`
	CompactionDebtConcurrency   uint64          `json:"compaction_debt_concurrency"`
	ReadSamplingMultiplier      int64           `json:"read_sampling_multiplier"`
	BytesPerSync                int             `json:"bytes_per_sync"`
	WALBytesPerSync             int             `json:"wal_bytes_per_sync"`
	LBaseMaxBytes               int64           `json:"l_base_max_bytes"`
	LevelMultiplier             int             `json:"level_multiplier"`
	Levels                      []ResolvedLevel `json:"levels"`
}

// ResolvedLevel is the effective per-level configuration.
type ResolvedLevel struct {
	Level                int    `json:"level"`
	TargetFileSize       int64  `json:"target_file_size"`
	Compression          string `json:"compression"`
	BlockSize            int    `json:"block_size"`
	BlockRestartInterval int    `json:"block_restart_interval"`
	BlockSizeThreshold   int    `json:"block_size_threshold"`
	IndexBlockSize       int    `json:"index_block_size"`
	FilterPolicy         string `json:"filter_policy"`
}
