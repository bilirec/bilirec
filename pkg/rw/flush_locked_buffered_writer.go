package rw

import (
	"io"
	"sync"
)

// FlushLockedBufferedWriter provides bufio-like buffering and only acquires
// the caller-provided lock during Flush. Write stays lock-free by design.
type FlushLockedBufferedWriter struct {
	w       io.Writer
	buf     []byte
	n       int
	flushMu sync.Locker
}

func NewFlushLockedBufferedWriter(w io.Writer, size int, flushMu sync.Locker) *FlushLockedBufferedWriter {
	if size <= 0 {
		size = 4096
	}
	return &FlushLockedBufferedWriter{
		w:       w,
		buf:     make([]byte, size),
		flushMu: flushMu,
	}
}

func (b *FlushLockedBufferedWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if b.n == len(b.buf) {
			// Buffer is full; must flush before accepting more data.
			if err := b.Flush(); err != nil {
				return written, err
			}
		}

		if b.n == 0 && len(p) >= len(b.buf) {
			// Large write, empty buffer: write directly under lock.
			b.flushMu.Lock()
			n, err := b.w.Write(p)
			b.flushMu.Unlock()
			written += n
			p = p[n:]
			if err != nil {
				return written, err
			}
			continue
		}

		n := copy(b.buf[b.n:], p)
		b.n += n
		written += n
		p = p[n:]
	}

	return written, nil
}

func (b *FlushLockedBufferedWriter) Flush() error {
	b.flushMu.Lock()
	defer b.flushMu.Unlock()
	for b.n > 0 {
		n, err := b.w.Write(b.buf[:b.n])
		if n > 0 {
			copy(b.buf, b.buf[n:b.n])
			b.n -= n
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (b *FlushLockedBufferedWriter) Buffered() int {
	return b.n
}

func (b *FlushLockedBufferedWriter) Available() int {
	return len(b.buf) - b.n
}

func (b *FlushLockedBufferedWriter) Size() int {
	return len(b.buf)
}
