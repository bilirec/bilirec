package processors

import (
	"context"
	"errors"

	"github.com/eric2788/bilirec/pkg/flv"
	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

// ErrVideoHeaderChanged is re-exported so callers in this package can match
// without importing pkg/flv directly.
var ErrVideoHeaderChanged = flv.ErrVideoHeaderChanged

// flvHeaderSplitDetectorProcessor is a thin pipeline wrapper around
// flv.HeaderChangeDetector. All parsing logic lives in pkg/flv.
type flvHeaderSplitDetectorProcessor struct {
	detector        *flv.HeaderChangeDetector
	log             *logrus.Entry
	seedVideoHeader []byte // pre-seeded initial video header tag bytes
}

// NewFlvHeaderSplitDetector returns a ProcessorInfo that signals pipe rotation
// via ErrVideoHeaderChanged whenever the AVC sequence header changes.
func NewFlvHeaderSplitDetector() *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-header-split-detector",
		&flvHeaderSplitDetectorProcessor{
			detector: flv.NewHeaderChangeDetector(),
		},
		pipeline.WithErrorStrategy[[]byte](pipeline.ReturnNextOnError),
	)
}

// NewFlvHeaderSplitDetectorSeeded returns a ProcessorInfo pre-seeded with the
// video sequence header from the prior segment. This ensures the detector can
// detect changes from the very first sequence header it sees in the new segment
// rather than treating it as the baseline "first" header.
// videoHeaderTag must be the full FLV tag bytes as returned in
// FlvHeaderChangedError.VideoHeaderTag.
func NewFlvHeaderSplitDetectorSeeded(videoHeaderTag []byte) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-header-split-detector",
		&flvHeaderSplitDetectorProcessor{
			detector:        flv.NewHeaderChangeDetector(),
			seedVideoHeader: videoHeaderTag,
		},
		pipeline.WithErrorStrategy[[]byte](pipeline.ReturnNextOnError),
	)
}

func (p *flvHeaderSplitDetectorProcessor) Open(ctx context.Context, log *logrus.Entry) error {
	p.log = log
	p.detector.Reset()
	if len(p.seedVideoHeader) > 0 {
		p.detector.SeedVideoHeader(p.seedVideoHeader)
	}
	return nil
}

func (p *flvHeaderSplitDetectorProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	err := p.detector.DetectChange(data)
	if err == nil {
		return data, nil
	}

	var headerChanged *flv.FlvHeaderChangedError
	if errors.As(err, &headerChanged) {
		log.Infof("🔀 video sequence header changed (SPS/PPS diff), triggering pipe rotation")
		// Only return bytes after the changed video seq-header tag.
		// Bytes before the changed tag belong to the old stream config and should
		// not be replayed into the next segment.
		if headerChanged.TagEnd >= 0 && headerChanged.TagEnd <= len(data) {
			after := data[headerChanged.TagEnd:]
			if len(after) == 0 {
				return nil, err
			}
			// RealtimeFixer expects each tag to be preceded by PrevTagSize (4 bytes).
			// Since we cut exactly after a tag boundary, inject a synthetic
			// PrevTagSize to keep parser alignment for replay into next segment.
			carried := make([]byte, 0, flv.PrevTagSizeBytes+len(after))
			carried = append(carried, 0, 0, 0, 0)
			carried = append(carried, after...)
			return carried, err
		}
		return nil, err
	}

	return data, err
}

func (p *flvHeaderSplitDetectorProcessor) Close() error {
	p.detector.Close()
	return nil
}
