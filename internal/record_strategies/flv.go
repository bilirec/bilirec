package record_strategies

import (
	"context"
	"errors"
	"time"

	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/processors"
	"github.com/eric2788/bilirec/pkg/flv"
	"github.com/eric2788/bilirec/pkg/pipeline"
)

const (
	flvStateVideoHdr = "videoHdr"
	flvStateAudioHdr = "audioHdr"
)

// FlvStrategy handles HTTP-FLV byte streams.
// It is a zero-invasion extraction of the logic previously inlined in rotate().
type FlvStrategy struct {
	sharedFixer *flv.RealtimeFixer
}

func NewFlvStrategy() *FlvStrategy {
	return &FlvStrategy{
		sharedFixer: flv.NewRealtimeFixer(),
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

func (s *FlvStrategy) HandleErr(err error) ErrHandleResult {
	var headerChanged *flv.FlvHeaderChangedError
	if errors.As(err, &headerChanged) {
		// Reset timestamp tracking so the new segment's timestamps start from 0.
		s.sharedFixer.ResetTimestampStore()
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
	return nil
}
