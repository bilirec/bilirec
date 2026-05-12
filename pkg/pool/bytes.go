package pool

import (
	"sync"
)

const DefaultBufferSize = 5 * 1024 * 1024 // 5MB

type BytesPool struct {
	*sync.Pool
	BufferSize int
}

func NewBytesPool(bufferSize int) *BytesPool {
	return &BytesPool{
		BufferSize: bufferSize,
		Pool: &sync.Pool{
			New: func() any {
				return make([]byte, bufferSize)
			},
		},
	}
}

func (p *BytesPool) GetBytes() []byte {
	return p.Get().([]byte)
}

func (p *BytesPool) PutBytes(buf []byte) {
	if buf == nil || cap(buf) != p.BufferSize {
		return
	}
	p.Pool.Put(buf[:cap(buf)])
}
