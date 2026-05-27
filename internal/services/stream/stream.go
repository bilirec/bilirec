package stream

import (
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("service", "stream")

type Service struct {
	pool *pool.BytesPool
}

func NewService() *Service {
	return &Service{
		pool: pool.NewBytesPool(512 * 1024), // 512KB buffer
	}
}

func (r *Service) Flush(buf []byte) {
	if cap(buf) == r.pool.BufferSize {
		r.pool.PutBytes(buf)
	}
}
