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
				buf := make([]byte, bufferSize)
				return &buf
			},
		},
	}
}

func (p *BytesPool) GetBytes() []byte {
	return *p.GetBytesPtr()
}

func (p *BytesPool) PutBytes(buf []byte) {
	p.PutBytesPtr(&buf)
}

func (p *BytesPool) GetBytesPtr() *[]byte {
	return p.Pool.Get().(*[]byte)
}

func (p *BytesPool) PutBytesPtr(buf *[]byte) {
	if buf == nil {
		return
	}
	if cap(*buf) != p.BufferSize {
		return
	}
	*buf = (*buf)[:cap(*buf)]
	p.Pool.Put(buf)
}
