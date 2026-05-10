package stream

import (
	"context"
	"io"
	"time"

	"github.com/go-resty/resty/v2"
)

func (r *Service) ReadFlvStream(resp *resty.Response, ctx context.Context) (<-chan []byte, error) {
	ch := make(chan []byte, 16) // ~8 MB buffer
	go r.read(ch, resp.RawBody(), ctx)
	return ch, nil
}

func (r *Service) read(ch chan<- []byte, stream io.ReadCloser, ctx context.Context) {
	defer stream.Close()
	defer close(ch)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			buf := r.pool.GetBytes()
			n, err := stream.Read(buf)
			if err == io.EOF {
				logger.Info("stream ended")
				r.Flush(buf)
				return
			} else if err != nil {
				logger.Errorf("error reading stream: %v", err)
				r.Flush(buf)
				return
			}
			if n > 0 {
				select {
				case ch <- buf[:n]:
				case <-ctx.Done():
					r.Flush(buf)
					return
				}
			} else {
				r.Flush(buf)
				time.Sleep(1 * time.Millisecond)
			}
		}
	}
}
