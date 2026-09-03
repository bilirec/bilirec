package sink

import "time"

const (
	defaultBatchBytes    = 64 << 10
	defaultFlushInterval = time.Second
	defaultBufferSize    = 4096
)

type Options struct {
	BufferSize    int
	BatchBytes    int
	FlushInterval time.Duration
	Overflow      OverflowPolicy
	Hooks         Hooks
}

func normalizeOptions(opts Options) Options {
	if opts.BufferSize <= 0 {
		opts.BufferSize = defaultBufferSize
	}
	if opts.BatchBytes <= 0 {
		opts.BatchBytes = defaultBatchBytes
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultFlushInterval
	}
	if opts.Overflow == "" {
		opts.Overflow = OverflowDrop
	}
	return opts
}
