package pool

import "bytes"

type BufferPool struct {
	maxCap int
	slot   *boundablePool[*bytes.Buffer]
}

func NewBufferPool(initialCap, maxCap int, options ...BufferPoolOption) *BufferPool {
	if initialCap < 0 {
		initialCap = 0
	}
	if maxCap < initialCap {
		maxCap = initialCap
	}

	cfg := applyPoolOptions(options, PoolBoundedConfig{
		Mode:     BufferPoolModeSoft,
		Capacity: 128,
	})

	newBuf := func() *bytes.Buffer {
		return bytes.NewBuffer(make([]byte, 0, initialCap))
	}

	return &BufferPool{
		maxCap: maxCap,
		slot: newBoundablePool(cfg, newBuf,
			func(b *bytes.Buffer) *bytes.Buffer { return b },
			func(b *bytes.Buffer) (*bytes.Buffer, bool) {
				if b == nil || b.Cap() > maxCap {
					return b, false
				}
				b.Reset()
				return b, true
			},
		),
	}
}

func (bp *BufferPool) Get() *bytes.Buffer {
	return bp.slot.get()
}

func (bp *BufferPool) Put(buf *bytes.Buffer) {
	bp.slot.put(buf)
}
