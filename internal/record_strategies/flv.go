package record_strategies

import (
	"context"
	"errors"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
)

const (
	flvStateVideoHdr = "videoHdr"
	flvStateAudioHdr = "audioHdr"
)

// FlvStrategy handles HTTP-FLV byte streams.
// It is a zero-invasion extraction of the logic previously inlined in rotate().
type FlvStrategy struct {
	sharedFixer            *flv.RealtimeFixer
	writerPool             *pool.BucketedBytesPool
	releaseWriterPool      func()
	releaseParseBufferPool func()
}

func NewFlvStrategy(qn int) *FlvStrategy {
	writerPool, releaseWriterPool := acquireWriterPool(qn)
	parsePool, releaseParsePool := acquireParseBufferPool(qn)
	readBufSize := config.ReadStreamBytesPoolSizeForQn(qn)
	return &FlvStrategy{
		sharedFixer: flv.NewRealtimeFixer(
			flv.WithBufferPool(parsePool),
			flv.WithBufferSizes(readBufSize, readBufSize),
		),
		writerPool:             writerPool,
		releaseWriterPool:      releaseWriterPool,
		releaseParseBufferPool: releaseParsePool,
	}
}

func (s *FlvStrategy) FileExtension() string { return ".flv" }

func (s *FlvStrategy) BuildPipeline(ctx context.Context, outputPath string, state *RotationState) (*pipeline.Pipe[[]byte], error) {
	videoHdr := state.Data[flvStateVideoHdr]
	audioHdr := state.Data[flvStateAudioHdr]

	pipe := pipeline.New(
		processors.NewFlvStreamFixerWithFixer(s.sharedFixer),
		processors.NewFlvHeaderSplitDetectorSeeded(videoHdr),
		processors.NewFlvHeaderWriter(videoHdr, audioHdr),
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

func (s *FlvStrategy) HandleErr(err error) ErrHandleResult {
	var headerChanged *flv.FlvHeaderChangedError
	if errors.As(err, &headerChanged) {
		// Reset per-stream fixer state on rotation:
		// - timestamps should restart from 0 in the new segment
		// - dedup cache should not suppress fresh leading tags across segment boundary
		s.sharedFixer.ResetTimestampStore()
		s.sharedFixer.ResetDedupCache()
		state := &RotationState{
			Data: map[string][]byte{
				flvStateVideoHdr: headerChanged.VideoHeaderTag,
				flvStateAudioHdr: headerChanged.AudioHeaderTag,
			},
		}
		return ErrHandleResult{Action: ErrActionRotate, State: state}
	}

	if errors.Is(err, processors.ErrNotFlvFile) {
		return ErrHandleResult{Action: ErrActionAbort, AbortDelay: 5 * time.Second}
	}

	return ErrHandleResult{Action: ErrActionAbort}
}

func (s *FlvStrategy) Close() error {
	s.sharedFixer.Close()
	if s.releaseParseBufferPool != nil {
		s.releaseParseBufferPool()
		s.releaseParseBufferPool = nil
	}
	if s.releaseWriterPool != nil {
		s.releaseWriterPool()
		s.releaseWriterPool = nil
		s.writerPool = nil
	}
	return nil
}
