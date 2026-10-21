package preview

import "io"

type chunkReader struct {
	ch    <-chan []byte
	done  <-chan struct{}
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
	select {
	case next, ok := <-r.ch:
		if !ok {
			return nil, io.EOF
		}
		return next, nil
	default:
	}
	select {
	case next, ok := <-r.ch:
		if !ok {
			return nil, io.EOF
		}
		return next, nil
	case <-r.done:
		select {
		case next, ok := <-r.ch:
			if !ok || len(next) == 0 {
				return nil, io.EOF
			}
			return next, nil
		default:
			return nil, io.EOF
		}
	}
}
