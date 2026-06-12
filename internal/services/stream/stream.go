package stream

import (
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("service", "stream")

type Service struct {
	readPools          *pool.LazyDualPool[*pool.BytesPool]
	chanBufferSize     int
	highChanBufferSize int
}

func NewService(cfg *config.Config) *Service {
	highChanBufferSize := 48
	if config.ReadOnly != nil {
		highChanBufferSize = config.ReadOnly.ReadStreamChanBufferSizeHigh()
	}
	return &Service{
		readPools: pool.NewLazyDualPool(
			15*time.Minute,
			func() *pool.BytesPool { return pool.NewBytesPool(cfg.ReadStreamBytesPoolSize) },
			func() *pool.BytesPool { return pool.NewBytesPool(config.ReadOnly.ReadStreamBytesPoolSizeHigh()) },
		),
		chanBufferSize:     cfg.ReadStreamChanBufferSize,
		highChanBufferSize: highChanBufferSize,
	}
}

func (r *Service) Flush(buf []byte) {
	r.FlushTo(r.readPools.Default(), buf)
	highPool := r.readPools.MaybeHigh()
	if highPool != nil {
		r.FlushTo(highPool, buf)
	}
}

func (r *Service) FlushTo(p *pool.BytesPool, buf []byte) {
	if p == nil || buf == nil {
		return
	}
	if cap(buf) == p.BufferSize {
		p.PutBytes(buf)
	}
}

func (r *Service) acquireReadPool(qn int) (*pool.BytesPool, func()) {
	return r.readPools.Acquire(config.IsHighQualityQn(qn))
}

func (r *Service) chanBufferSizeForQn(qn int) int {
	if config.IsHighQualityQn(qn) {
		return r.highChanBufferSize
	}
	return r.chanBufferSize
}
