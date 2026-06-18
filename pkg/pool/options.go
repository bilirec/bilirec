package pool

type BufferPoolMode int

const (
	// BufferPoolModeSoft uses sync.Pool semantics (default behavior).
	BufferPoolModeSoft BufferPoolMode = iota
	// BufferPoolModeBounded keeps at most BoundedCapacity buffers in a fixed queue.
	BufferPoolModeBounded
)

type PoolBoundedConfig struct {
	Mode     BufferPoolMode
	Capacity int
}

type PoolOption func(*PoolBoundedConfig)

func WithPoolBoundedMode(enabled bool) PoolOption {
	return func(c *PoolBoundedConfig) {
		if enabled {
			c.Mode = BufferPoolModeBounded
		} else {
			c.Mode = BufferPoolModeSoft
		}
	}
}

func WithPoolBoundedCapacity(capacity int) PoolOption {
	return func(c *PoolBoundedConfig) {
		if capacity <= 0 {
			c.Capacity = 4
		} else {
			c.Capacity = capacity
		}
	}
}

// BufferPoolOption aliases PoolOption for backward compatibility.
type BufferPoolOption = PoolOption

func WithBoundedMode(enabled bool) BufferPoolOption {
	return WithPoolBoundedMode(enabled)
}

// WithBoundedCapacity preserves BufferPool semantics: non-positive capacity falls back to 128.
func WithBoundedCapacity(capacity int) BufferPoolOption {
	return func(c *PoolBoundedConfig) {
		if capacity <= 0 {
			c.Capacity = 128
		} else {
			c.Capacity = capacity
		}
	}
}

type BytesPoolOption = PoolOption
type BucketedBytesPoolOption = PoolOption

func applyPoolOptions(opts []PoolOption, defaults PoolBoundedConfig) PoolBoundedConfig {
	cfg := defaults
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

func boundableCapacity(cfg PoolBoundedConfig) int {
	capacity := cfg.Capacity
	if capacity <= 0 {
		return 4
	}
	return capacity
}
