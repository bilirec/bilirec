package pool

import (
	"bytes"
	"sync"
)

type BufferPoolMode int

const (
	// BufferPoolModeSoft uses sync.Pool semantics (default behavior).
	BufferPoolModeSoft BufferPoolMode = iota
	// BufferPoolModeBounded keeps at most BoundedCapacity buffers in a fixed queue.
	BufferPoolModeBounded
)

type BufferPoolConfig struct {
	mode            BufferPoolMode
	boundedCapacity int
}

type BufferPoolOption func(*BufferPoolConfig)

type BufferPool struct {
	pool    sync.Pool
	maxCap  int
	mode    BufferPoolMode
	bounded chan *bytes.Buffer
	newBuf  func() *bytes.Buffer
}

func NewBufferPool(initialCap, maxCap int, options ...BufferPoolOption) *BufferPool {
	if initialCap < 0 {
		initialCap = 0
	}
	if maxCap < initialCap {
		maxCap = initialCap
	}

	cfg := BufferPoolConfig{
		mode:            BufferPoolModeSoft,
		boundedCapacity: 128,
	}
	for _, opt := range options {
		if opt != nil {
			opt(&cfg)
		}
	}

	newBuf := func() *bytes.Buffer {
		return bytes.NewBuffer(make([]byte, 0, initialCap))
	}

	bp := &BufferPool{
		maxCap: maxCap,
		mode:   cfg.mode,
		newBuf: newBuf,
	}

	if cfg.mode == BufferPoolModeBounded {
		bp.bounded = make(chan *bytes.Buffer, cfg.boundedCapacity)
		return bp
	}

	bp.pool = sync.Pool{
		New: func() any {
			return newBuf()
		},
	}

	return bp
}

func (bp *BufferPool) Get() *bytes.Buffer {
	if bp.mode == BufferPoolModeBounded {
		select {
		case buf := <-bp.bounded:
			return buf
		default:
			return bp.newBuf()
		}
	}

	return bp.pool.Get().(*bytes.Buffer)
}

func (bp *BufferPool) Put(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	// Only pool buffers that haven't grown too large
	if buf.Cap() <= bp.maxCap {
		buf.Reset()
		if bp.mode == BufferPoolModeBounded {
			select {
			case bp.bounded <- buf:
			default:
				// Queue is full; let buffer be garbage collected.
			}
			return
		}
		bp.pool.Put(buf)
	}
	// Otherwise, let it be garbage collected
}

func WithBoundedMode(enabled bool) BufferPoolOption {
	return func(c *BufferPoolConfig) {
		if enabled {
			c.mode = BufferPoolModeBounded
		} else {
			c.mode = BufferPoolModeSoft
		}
	}
}

func WithBoundedCapacity(capacity int) BufferPoolOption {
	return func(c *BufferPoolConfig) {
		if capacity <= 0 {
			c.boundedCapacity = 128
		} else {
			c.boundedCapacity = capacity
		}
	}
}
