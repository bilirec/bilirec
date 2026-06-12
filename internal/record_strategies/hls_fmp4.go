package record_strategies

import (
	"context"
	"errors"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

// HlsFmp4Strategy handles HLS fragmented MP4 byte streams.
// Each []byte received from ReadHlsStream is a complete fMP4 segment:
//   - First segment: ftyp+moov (init segment, box type at bytes[4:8] == "ftyp")
//   - Subsequent segments: moof+mdat (media fragments)
//
// Appending all segments in order produces a valid fragmented MP4 source file
// (.fmp4), which can then be remuxed to seek-friendly .mp4 in finalize flow.
type HlsFmp4Strategy struct {
	bases map[uint32]uint64
}

func NewHlsFmp4Strategy() *HlsFmp4Strategy {
	return &HlsFmp4Strategy{
		bases: make(map[uint32]uint64),
	}
}

func (s *HlsFmp4Strategy) FileExtension() string { return ".fmp4" }

func (s *HlsFmp4Strategy) BuildPipeline(ctx context.Context, outputPath string, state *RotationState) (*pipeline.Pipe[[]byte], error) {
	pipe := pipeline.New(
		processors.NewSegmentDedup(),
		processors.NewFmp4BoxGuard(&s.bases),
		processors.NewFmp4TimestampNormalizer(&s.bases),
		processors.NewBufferedStreamWriter(
			outputPath,
			processors.WithBufferSize(config.ReadOnly.LiveStreamWriterBufferSize()),
			processors.WithSkipSmallFlushThreshold(config.ReadOnly.SkipSmallFlushThreshold()),
			processors.WithSyncPeriod(time.Duration(config.ReadOnly.LiveStreamWriterSyncPeriodSecs())*time.Second),
			processors.WithFlushPeriod(time.Duration(config.ReadOnly.LiveStreamWriterFlushPeriodSecs())*time.Second),
			processors.WithChanBufferSize(config.ReadOnly.LiveStreamWriterChanBufferSize()),
			processors.WithBytesPool(getWriterBytesPool()),
			processors.WithSDCardProtection(config.ReadOnly.SkipSmallFlush()),
			processors.WithSequentialWrite(config.ReadOnly.SequentialWrite()),
		),
	)
	return pipe, nil
}

func (s *HlsFmp4Strategy) HandleErr(err error) ErrHandleResult {
	// Handle stream discontinuity: when a new init segment (ftyp) appears
	// after media segments, we need to rotate to a new file to maintain
	// valid fmp4 file structure (each file should have exactly one init segment)
	if errors.Is(err, processors.ErrFmp4Discontinuity) {
		// Reset timestamp bases for the new segment
		s.bases = make(map[uint32]uint64)
		return ErrHandleResult{Action: ErrActionRotate}
	}

	return ErrHandleResult{Action: ErrActionAbort}
}

func (s *HlsFmp4Strategy) Close() error { return nil }
