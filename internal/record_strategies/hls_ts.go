package record_strategies

import (
	"context"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
)

// HlsTsStrategy handles HLS MPEG-TS byte streams.
// Each []byte received from ReadHlsStream is a complete .ts segment.
// Conversion to .mp4 is handled by the recorder's finalize() like FLV.
type HlsTsStrategy struct {
	writerPool        *pool.BucketedBytesPool
	releaseWriterPool func()
}

func NewHlsTsStrategy(qn int) *HlsTsStrategy {
	writerPool, releaseWriterPool := acquireWriterPool(qn)
	return &HlsTsStrategy{
		writerPool:        writerPool,
		releaseWriterPool: releaseWriterPool,
	}
}

func (s *HlsTsStrategy) FileExtension() string { return ".ts" }

func (s *HlsTsStrategy) BuildPipeline(ctx context.Context, outputPath string, state *RotationState) (*pipeline.Pipe[[]byte], error) {
	pipe := pipeline.New(
		processors.NewSegmentDedup(),
		processors.NewTsContinuityFixer(),
		processors.NewBufferedStreamWriter(
			outputPath,
			processors.WithBufferSize(config.ReadOnly.LiveStreamWriterBufferSize()),
			processors.WithSkipSmallFlushThreshold(config.ReadOnly.SkipSmallFlushThreshold()),
			processors.WithSyncPeriod(time.Duration(config.ReadOnly.LiveStreamWriterSyncPeriodSecs())*time.Second),
			processors.WithColdCacheReleasePeriod(time.Duration(config.ReadOnly.LiveStreamWriterColdCacheReleaseSecs())*time.Second),
			processors.WithFlushPeriod(time.Duration(config.ReadOnly.LiveStreamWriterFlushPeriodSecs())*time.Second),
			processors.WithChanBufferSize(config.ReadOnly.LiveStreamWriterChanBufferSize()),
			processors.WithBytesPool(s.writerPool),
			processors.WithSDCardProtection(config.ReadOnly.SkipSmallFlush()),
			processors.WithDropFilePageCache(config.ReadOnly.DropFilePageCache()),
			processors.WithSequentialWrite(config.ReadOnly.SequentialWrite()),
		),
	)
	return pipe, nil
}

func (s *HlsTsStrategy) HandleErr(err error) ErrHandleResult {
	return ErrHandleResult{Action: ErrActionAbort}
}

func (s *HlsTsStrategy) Close() error {
	if s.releaseWriterPool != nil {
		s.releaseWriterPool()
		s.releaseWriterPool = nil
		s.writerPool = nil
	}
	return nil
}
