package pool

import "sync"

// BytesSlicePool manages variable-size byte slices with capacity limits.
// Unlike BytesPool which requires fixed sizes, this pool accepts slices
// with capacity <= maxCap and automatically discards oversized buffers.
type BytesSlicePool struct {
	pool    sync.Pool
	maxCap  int
	initCap int
}

// NewBytesSlicePool creates a new variable-size bytes slice pool.
// initCap: initial capacity for newly allocated slices.
// maxCap: maximum capacity to pool; slices larger than this are discarded (GC'd).
func NewBytesSlicePool(initCap, maxCap int) *BytesSlicePool {
	if initCap < 0 {
		initCap = 0
	}
	if maxCap < initCap {
		maxCap = initCap
	}
	return &BytesSlicePool{
		initCap: initCap,
		maxCap:  maxCap,
		pool: sync.Pool{
			New: func() any {
				return make([]byte, 0, initCap)
			},
		},
	}
}

// Get retrieves a slice from the pool and resets its length to 0.
// The capacity is preserved for reuse.
func (p *BytesSlicePool) Get() []byte {
	b := p.pool.Get().([]byte)
	return b[:0]
}

// GetSized retrieves a slice and ensures its length is exactly size.
// If the pooled slice capacity is insufficient, a new slice is allocated.
func (p *BytesSlicePool) GetSized(size int) []byte {
	if size < 0 {
		size = 0
	}
	b := p.Get()
	if cap(b) < size {
		p.Put(b)
		return make([]byte, size)
	}
	return b[:size]
}

// Put returns a slice to the pool if its capacity is within limits.
// Slices with capacity > maxCap are not pooled (left for GC).
func (p *BytesSlicePool) Put(b []byte) {
	if b == nil || cap(b) > p.maxCap {
        return
    }
    p.pool.Put(b[:0])
}
