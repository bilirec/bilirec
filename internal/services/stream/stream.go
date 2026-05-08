package stream

import (
	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/pkg/pool"
	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("service", "stream")

type Service struct {
	pool             *pool.BytesPool
	hlsClientFactory func() *resty.Client
}

func NewService(bilic *bilibili.Client) *Service {
	return &Service{
		pool:             pool.NewBytesPool(256 * 1024),
		hlsClientFactory: bilic.NewLiveHlsClient,
	}
}

func (r *Service) Flush(buf []byte) {
	if cap(buf) == r.pool.BufferSize {
		r.pool.PutBytes(buf)
	}
}

func (r *Service) newHlsClient() *resty.Client {
	if r.hlsClientFactory != nil {
		return r.hlsClientFactory()
	}
	return resty.New()
}
