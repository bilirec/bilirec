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
