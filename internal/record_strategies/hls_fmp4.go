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

// HlsFmp4Strategy handles HLS fragmented MP4 byte streams.
// Each []byte received from ReadHlsStream is a complete fMP4 segment:
//   - First segment: ftyp+moov (init segment, box type at bytes[4:8] == "ftyp")
//   - Subsequent segments: moof+mdat (media fragments)
//
// Appending all segments in order produces a valid fragmented MP4 source file
// (.fmp4), which can then be remuxed to seek-friendly .mp4 in finalize flow.
type HlsFmp4Strategy struct {
	bases             map[uint32]uint64
	lastInit          []byte
	writerPool        *pool.BucketedBytesPool
	releaseWriterPool func()
}

func NewHlsFmp4Strategy(qn int) *HlsFmp4Strategy {
	writerPool, releaseWriterPool := acquireWriterPool(qn)
	return &HlsFmp4Strategy{
		bases:             make(map[uint32]uint64),
		writerPool:        writerPool,
		releaseWriterPool: releaseWriterPool,
	}
}

func (s *HlsFmp4Strategy) FileExtension() string { return ".fmp4" }

func (s *HlsFmp4Strategy) BuildPipeline(ctx context.Context, outputPath string, state *RotationState) (*pipeline.Pipe[[]byte], error) {
	pendingInit := state.Data[fmp4StatePendingInit]
	pipe := pipeline.New(
		processors.NewSegmentDedup(),
		processors.NewFmp4BoxGuard(&s.bases, &s.lastInit),
		processors.NewFmp4TimestampNormalizer(&s.bases),
		processors.NewFmp4InitWriter(pendingInit),
		processors.NewBufferedStreamWriter(
			outputPath,
			processors.WithBufferSize(config.ReadOnly.LiveStreamWriterBufferSize()),
			processors.WithSkipSmallFlushThreshold(config.ReadOnly.SkipSmallFlushThreshold()),
			processors.WithSyncPeriod(time.Duration(config.ReadOnly.LiveStreamWriterSyncPeriodSecs())*time.Second),
			processors.WithFlushPeriod(time.Duration(config.ReadOnly.LiveStreamWriterFlushPeriodSecs())*time.Second),
			processors.WithChanBufferSize(config.ReadOnly.LiveStreamWriterChanBufferSize()),
			processors.WithBytesPool(s.writerPool),
			processors.WithSDCardProtection(config.ReadOnly.SkipSmallFlush()),
			processors.WithSequentialWrite(config.ReadOnly.SequentialWrite()),
		),
	)
	return pipe, nil
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
