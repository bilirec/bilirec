package processors

import (
	"context"
	"sync"

	"github.com/eric2788/bilirec/pkg/flv"
	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

type flvHeaderWriterProcessor struct {
	writer  *flv.FlvHeaderWriter
	mu      sync.Mutex
	written bool
}

// NewFlvHeaderWriter returns a processor that prepends an FLV file preamble
// (and optional sequence-header tags) to the very first data chunk of a
// segment. videoHeaderTag and audioHeaderTag may be nil for segment 0.
func NewFlvHeaderWriter(videoHeaderTag, audioHeaderTag []byte) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-header-writer",
		&flvHeaderWriterProcessor{
			writer: &flv.FlvHeaderWriter{
				VideoHeaderTag: videoHeaderTag,
				AudioHeaderTag: audioHeaderTag,
			},
		},
	)
}

func (p *flvHeaderWriterProcessor) Open(ctx context.Context, log *logrus.Entry) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = false
	return nil
}

func (p *flvHeaderWriterProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.written {
		return data, nil
	}
	p.written = true
	return p.writer.Prepend(data), nil
}

func (p *flvHeaderWriterProcessor) Close() error {
	return nil
}
