package flv

import "github.com/bilirec/bilirec/pkg/pool"

// RealtimeFixerOption configures buffer sizes for a RealtimeFixer instance.
type RealtimeFixerOption func(*realtimeFixerConfig)

type realtimeFixerConfig struct {
	bufferPool        *pool.BufferPool
	initialBufferSize int
	maxBufferSize     int
}

func defaultRealtimeFixerConfig() realtimeFixerConfig {
	return realtimeFixerConfig{
		initialBufferSize: DefaultBufferSize,
		maxBufferSize:     MaxBufferSize,
	}
}

func applyRealtimeFixerOptions(opts ...RealtimeFixerOption) realtimeFixerConfig {
	cfg := defaultRealtimeFixerConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return normalizeRealtimeFixerConfig(cfg)
}

// normalizeRealtimeFixerConfig clamps invalid values to package defaults.
func normalizeRealtimeFixerConfig(cfg realtimeFixerConfig) realtimeFixerConfig {
	if cfg.maxBufferSize <= 0 {
		cfg.maxBufferSize = MaxBufferSize
	}
	if cfg.initialBufferSize <= 0 {
		cfg.initialBufferSize = DefaultBufferSize
	}
	if cfg.initialBufferSize > cfg.maxBufferSize {
		cfg.initialBufferSize = cfg.maxBufferSize
	}
	return cfg
}

// normalizeRealtimeFixerBufferSizes clamps invalid buffer pool sizes.
func normalizeRealtimeFixerBufferSizes(initial, max int) realtimeFixerConfig {
	return normalizeRealtimeFixerConfig(realtimeFixerConfig{
		initialBufferSize: initial,
		maxBufferSize:     max,
	})
}

// WithBufferPool reuses a shared parse-buffer pool instead of creating a per-fixer pool.
func WithBufferPool(bp *pool.BufferPool) RealtimeFixerOption {
	return func(c *realtimeFixerConfig) {
		if bp != nil {
			c.bufferPool = bp
		}
	}
}

// WithBufferSizes sets the parse-buffer pool initial and max capacity.
// When only the max size is known (e.g. READ_STREAM_BYTES_POOL_SIZE), pass it
// for both arguments so pooled buffers match the stream read chunk size.
func WithBufferSizes(initial, max int) RealtimeFixerOption {
	return func(c *realtimeFixerConfig) {
		if initial > 0 {
			c.initialBufferSize = initial
		}
		if max > 0 {
			c.maxBufferSize = max
		}
	}
}

