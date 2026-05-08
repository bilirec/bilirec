package stream

import (
	"github.com/eric2788/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("service", "stream")

type Service struct {
	pool *pool.BytesPool
}

func NewService() *Service {
	return &Service{pool: pool.NewBytesPool(256 * 1024)}
}

func (r *Service) Flush(buf []byte) {
	if cap(buf) == r.pool.BufferSize {
		r.pool.PutBytes(buf)
	}
}
