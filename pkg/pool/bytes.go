package pool

const DefaultBufferSize = 5 * 1024 * 1024 // 5MB

type BytesPool struct {
	BufferSize int
	slot       *boundablePool[[]byte]
}

func NewBytesPool(bufferSize int, opts ...BytesPoolOption) *BytesPool {
	cfg := applyPoolOptions(opts, PoolBoundedConfig{
		Mode:     BufferPoolModeSoft,
		Capacity: 4,
	})
	size := bufferSize
	return &BytesPool{
		BufferSize: size,
		slot: newBoundablePool(cfg,
			func() []byte { return make([]byte, size) },
			func(b []byte) []byte { return b[:size] },
			func(b []byte) ([]byte, bool) {
				if cap(b) != size {
					return b, false
				}
				return b[:cap(b)], true
			},
		),
	}
}

func (p *BytesPool) GetBytes() []byte {
	return p.slot.get()
}

func (p *BytesPool) PutBytes(buf []byte) {
	p.slot.put(buf)
}

// GetSized returns a buffer that can hold size bytes. Requests up to the
// configured fixed size reuse the pool; larger requests fall back to a
// one-off allocation.
func (p *BytesPool) GetSized(size int) []byte {
	if size <= 0 {
		return []byte{}
	}
	if size <= p.BufferSize {
		return p.GetBytes()[:size]
	}
	return make([]byte, size)
}

// Put returns a buffer obtained from GetSized to the pool when it belongs to
// this fixed-size pool. Oversized one-off allocations are ignored.
func (p *BytesPool) Put(buf []byte) {
	p.PutBytes(buf)
}
