package record_strategies

import (
	"context"

	"github.com/eric2788/bilirec/internal/processors"
	"github.com/eric2788/bilirec/pkg/pipeline"
)

// HlsTsStrategy handles HLS MPEG-TS byte streams.
// Each []byte received from ReadHlsStream is a complete .ts segment.
// Conversion to .mp4 is handled by the recorder's finalize() like FLV.
type HlsTsStrategy struct{}

func NewHlsTsStrategy() *HlsTsStrategy {
	return &HlsTsStrategy{}
}

func (s *HlsTsStrategy) FileExtension() string { return ".ts" }

func (s *HlsTsStrategy) BuildPipeline(ctx context.Context, outputPath string, state *RotationState) (*pipeline.Pipe[[]byte], error) {
	pipe := pipeline.New(
		processors.NewSegmentDedup(),
		processors.NewTsContinuityFixer(),
		processors.NewTsSegmentWriter(outputPath),
	)
	return pipe, nil
}

func (s *HlsTsStrategy) HandleErr(err error) ErrHandleResult {
	return ErrHandleResult{Action: ErrActionAbort}
}

func (s *HlsTsStrategy) Close() error { return nil }
