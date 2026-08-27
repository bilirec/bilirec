package stream

import (
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/pool"
)

var log = logger.Named("stream")

type Service struct {
	chunkPools         *pool.LazyDualPool[*pool.BucketedBytesPool]
	chanBufferSize     int
	highChanBufferSize int
}

func NewService(cfg *config.Config) *Service {
	highChanBufferSize := config.ReadOnly.ReadStreamChanBufferSizeHigh()
	boundedHigh := config.ReadStreamBytesPoolBoundedCapacity(highChanBufferSize)
	bounded := config.ReadStreamBytesPoolBoundedCapacity(cfg.ReadStreamChanBufferSize)
	return &Service{
		chunkPools: pool.NewLazyDualPool(
			15*time.Minute,
			func() *pool.BucketedBytesPool {
				return newChunkBytesPool(cfg.ReadStreamBytesPoolSize, bounded)
			},
			func() *pool.BucketedBytesPool {
				return newChunkBytesPool(
					config.ReadOnly.ReadStreamBytesPoolSizeHigh(),
					boundedHigh,
				)
			},
		),
		chanBufferSize:     cfg.ReadStreamChanBufferSize,
		highChanBufferSize: highChanBufferSize,
	}
}

func newChunkBytesPool(baseSize, boundedCap int) *pool.BucketedBytesPool {
	if baseSize <= 0 {
		baseSize = 512 * 1024
	}
	return pool.NewBucketedBytesPool(baseSize,
		pool.WithPoolBoundedMode(true),
		pool.WithPoolBoundedCapacity(boundedCap),
	)
}

// AcquireChunkPool returns the session chunk pool and a release callback for the
// LazyDualPool high-tier lease. The read goroutine must call release when it exits.
func (r *Service) AcquireChunkPool(qn int) (*pool.BucketedBytesPool, func()) {
	return r.chunkPools.Acquire(config.IsHighQualityQn(qn))
}

func (r *Service) readBufSizeForQn(qn int) int {
	return config.ReadStreamBytesPoolSizeForQn(qn)
}

func (r *Service) putChunk(pool *pool.BucketedBytesPool, buf []byte) {
	if pool == nil || buf == nil || cap(buf) == 0 {
		return
	}
	pool.Put(buf[:cap(buf)])
}

func (r *Service) chanBufferSizeForQn(qn int) int {
	if config.IsHighQualityQn(qn) {
		return r.highChanBufferSize
	}
	return r.chanBufferSize
}
