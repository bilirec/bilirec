package stream

import (
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("service", "stream")

type Service struct {
	pool           *pool.BytesPool
	chanBufferSize int
}

func NewService(cfg *config.Config) *Service {
	return &Service{
		pool:           pool.NewBytesPool(cfg.ReadStreamBytesPoolSize),
		chanBufferSize: cfg.ReadStreamChanBufferSize,
	}
}

func (r *Service) Flush(buf []byte) {
	if cap(buf) == r.pool.BufferSize {
		r.pool.PutBytes(buf)
	}
}
