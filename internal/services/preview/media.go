package preview

import (
	"io"
	"sync"
)

// mediaMaxQueuedBytes is how far a viewer may lag before being kicked.
// Count bytes, not channel slots: ingest chunks are often 8–64KB.
const mediaMaxQueuedBytes = 1 << 20

type mediaQ struct {
	mu     sync.Mutex
	cond   *sync.Cond
	chunks [][]byte
	nbytes int
	closed bool
}

type chunkReader struct {
	q     *mediaQ
	cur   []byte
	leave func()
}

func (q *mediaQ) tryPush(b []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.nbytes+len(b) > mediaMaxQueuedBytes {
		return false
	}
	q.chunks = append(q.chunks, b)
	q.nbytes += len(b)
	q.cond.Signal()
	return true
}

func (q *mediaQ) pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.chunks) == 0 {
		return nil, false
	}
	b := q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	q.nbytes -= len(b)
	return b, true
}

func (q *mediaQ) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.cond.Broadcast()
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.cur) == 0 {
		next, err := r.nextChunk()
		if err != nil {
			return 0, err
		}
		r.cur = next
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

func (r *chunkReader) Close() error {
	if r.leave != nil {
		r.leave()
		r.leave = nil
	}
	return nil
}

func (r *chunkReader) nextChunk() ([]byte, error) {
	next, ok := r.q.pop()
	if !ok {
		return nil, io.EOF
	}
	return next, nil
}

func newMediaQ() *mediaQ {
	q := &mediaQ{}
	q.cond = sync.NewCond(&q.mu)
	return q
}
