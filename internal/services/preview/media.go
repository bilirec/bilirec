package preview

import (
	"io"

	"github.com/bilirec/bilirec/pkg/ds"
)

type chunkReader struct {
	q     *ds.ChunkQueue[[]byte]
	cur   []byte
	leave func()
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
	next, ok := r.q.Pop()
	if !ok {
		return nil, io.EOF
	}
	return next, nil
}

func newMediaQ(maxBytes int) *ds.ChunkQueue[[]byte] {
	return ds.NewChunkQueue(maxBytes, func(b []byte) int { return len(b) })
}
