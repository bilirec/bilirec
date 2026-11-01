package record_strategies

import (
	"context"
	"errors"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
)

const fmp4StatePendingInit = "pendingInit"

type HlsFmp4Strategy struct {
	bases             map[uint32]uint64
	lastInit          []byte
	writerPool        *pool.BucketedBytesPool
	releaseWriterPool func()
}

func NewHlsFmp4Strategy(qn int) RecordStrategy {
	writerPool, releaseWriterPool := acquireWriterPool(qn)
	return &HlsFmp4Strategy{
		bases:             make(map[uint32]uint64),
		writerPool:        writerPool,
		releaseWriterPool: releaseWriterPool,
	}
}

func (s *HlsFmp4Strategy) FileExtension() string { return ".fmp4" }

func (s *HlsFmp4Strategy) BuildPipeline(ctx context.Context, outputPath string, state *RotationState, hooks PipelineHooks) (*pipeline.Pipe[[]byte], error) {
	pendingInit := state.Data[fmp4StatePendingInit]
	var onGap func(processors.TimelineGap)
	if hooks.OnTimelineGap != nil {
		onGap = hooks.OnTimelineGap
	}
	return pipeline.New(
		processors.NewSegmentDedup(),
		processors.NewFmp4BoxGuard(&s.bases, &s.lastInit),
		processors.NewFmp4TimestampNormalizer(&s.bases, onGap),
		processors.NewFmp4InitWriter(pendingInit),
		processors.NewBufferedStreamWriter(
			outputPath,
			processors.WithBufferSize(config.ReadOnly.LiveStreamWriterBufferSize()),
			processors.WithSkipSmallFlushThreshold(config.ReadOnly.SkipSmallFlushThreshold()),
			processors.WithSyncPeriod(time.Duration(config.ReadOnly.LiveStreamWriterSyncPeriodSecs())*time.Second),
			processors.WithColdCacheReleasePeriod(time.Duration(config.ReadOnly.LiveStreamWriterColdCacheReleaseSecs())*time.Second),
			processors.WithFlushPeriod(time.Duration(config.ReadOnly.LiveStreamWriterFlushPeriodSecs())*time.Second),
			processors.WithChanBufferSize(config.ReadOnly.LiveStreamWriterChanBufferSize()),
			processors.WithBytesPool(s.writerPool),
			processors.WithDropFilePageCache(config.ReadOnly.DropFilePageCache()),
			processors.WithSequentialWrite(config.ReadOnly.SequentialWrite()),
			processors.WithOnBytesWritten(hooks.OnBytesWritten),
			processors.WithOnFlush(hooks.OnFlush),
			processors.WithOnSync(hooks.OnSync),
			processors.WithOnEnqueueWait(hooks.OnEnqueueWait),
		),
	), nil
}

func (s *HlsFmp4Strategy) HandleErr(err error) ErrHandleResult {
	var disc *processors.Fmp4DiscontinuityError
	if errors.As(err, &disc) {
		s.bases = make(map[uint32]uint64)
		return ErrHandleResult{
			Action: ErrActionRotate,
			State: &RotationState{
				Data: map[string][]byte{
					fmp4StatePendingInit: disc.InitSegment,
				},
			},
		}
	}

	return ErrHandleResult{Action: ErrActionAbort}
}

func (s *HlsFmp4Strategy) Close() error {
	if s.releaseWriterPool != nil {
		s.releaseWriterPool()
		s.releaseWriterPool = nil
		s.writerPool = nil
	}
	return nil
}
