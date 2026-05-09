package record_strategies

import (
	"context"

	"github.com/eric2788/bilirec/internal/processors"
	"github.com/eric2788/bilirec/pkg/pipeline"
)

// HlsFmp4Strategy handles HLS fragmented MP4 byte streams.
// Each []byte received from ReadHlsStream is a complete fMP4 segment:
//   - First segment: ftyp+moov (init segment, box type at bytes[4:8] == "ftyp")
//   - Subsequent segments: moof+mdat (media fragments)
//
// Appending all segments in order produces a valid fragmented MP4 source file
// (.fmp4), which can then be remuxed to seek-friendly .mp4 in finalize flow.
type HlsFmp4Strategy struct{}

func NewHlsFmp4Strategy() *HlsFmp4Strategy {
	return &HlsFmp4Strategy{}
}

func (s *HlsFmp4Strategy) FileExtension() string { return ".fmp4" }

func (s *HlsFmp4Strategy) BuildPipeline(ctx context.Context, outputPath string, state *RotationState) (*pipeline.Pipe[[]byte], error) {
	bases := make(map[uint32]uint64)
	pipe := pipeline.New(
		processors.NewSegmentDedup(),
		processors.NewFmp4BoxGuard(&bases),
		processors.NewFmp4SegmentWriter(outputPath, &bases),
	)
	return pipe, nil
}

func (s *HlsFmp4Strategy) HandleErr(err error) ErrHandleResult {
	return ErrHandleResult{Action: ErrActionAbort}
}

func (s *HlsFmp4Strategy) Close() error { return nil }
