package processors

import (
	"context"

	"github.com/eric2788/bilirec/pkg/hls"
	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

// TsContinuityFixerProcessor repairs MPEG-TS continuity counters at segment
// boundaries.
//
// Each MPEG-TS packet carries a 4-bit continuity counter (CC) per PID that
// must increment by 1 (mod 16) between consecutive packets of the same PID.
// When two independently-encoded HLS segments are concatenated, the CC of the
// first packet of the new segment may not follow on from the CC of the last
// packet of the previous segment.  Players tolerate small gaps but repeated
// discontinuities can cause A/V desync or decoder errors in strict decoders.
//
// This processor patches the CC values of every packet in each incoming
// segment so that they continue seamlessly from the previous segment.
//
// Note: packets with the discontinuity_indicator bit set (adaptation field
// bit 0x80) are exempt — their counter reset is intentional.
type TsContinuityFixerProcessor struct {
	fixer *hls.TsContinuityFixer
}

func NewTsContinuityFixer() *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"ts-continuity-fixer",
		&TsContinuityFixerProcessor{},
	)
}

func (p *TsContinuityFixerProcessor) Open(_ context.Context, _ *logrus.Entry) error {
	p.fixer = hls.NewTsContinuityFixer()
	return nil
}

// Process rewrites continuity counters in place and returns the (modified) data.
func (p *TsContinuityFixerProcessor) Process(_ context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if p.fixer == nil {
		p.fixer = hls.NewTsContinuityFixer()
	}

	result := p.fixer.FixSegment(data)
	if result.Patched > 3 {
		log.Debugf("ts-continuity-fixer: patched %d packets in segment (%d B)", result.Patched, len(data))
	} else if result.Patched > 10 {
		log.Warnf("ts-continuity-fixer: patched %d packets in segment (%d B) - this may indicate a problem with the source segments", result.Patched, len(data))
	}
	return data, nil
}

func (p *TsContinuityFixerProcessor) Close() error { return nil }
