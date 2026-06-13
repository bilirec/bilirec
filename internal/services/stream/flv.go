package stream

import (
	"context"
	"io"
	"time"

	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/go-resty/resty/v2"
)

func (r *Service) ReadFlvStream(resp *resty.Response, ctx context.Context, qn int) (<-chan []byte, error) {
	bytesPool, releasePool := r.acquireReadPool(qn)
	ch := make(chan []byte, r.chanBufferSizeForQn(qn))
	go r.read(ch, resp.RawBody(), ctx, bytesPool, releasePool)
	return ch, nil
}

func (r *Service) read(ch chan<- []byte, stream io.ReadCloser, ctx context.Context, bytesPool *pool.BytesPool, releasePool func()) {
	defer stream.Close()
	defer close(ch)
	defer releasePool()
	for {
		select {
		case <-ctx.Done():
			return
		default:
			buf := bytesPool.GetBytes()
			n, err := stream.Read(buf)
			if err == io.EOF {
				logger.Info("直播流已结束")
				r.FlushTo(bytesPool, buf)
				return
			} else if err != nil {
				logger.Errorf("读取直播流失败：%v", err)
				r.FlushTo(bytesPool, buf)
				return
			}
			if n > 0 {
				select {
				case ch <- buf[:n]:
				case <-ctx.Done():
					r.FlushTo(bytesPool, buf)
					return
				}
			} else {
				r.FlushTo(bytesPool, buf)
				time.Sleep(1 * time.Millisecond)
			}
		}
	}
}
