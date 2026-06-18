package processors

import (
	"context"
	"sync"

	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

type fmp4InitWriterProcessor struct {
	pendingInit []byte
	mu          sync.Mutex
	written     bool
}

// NewFmp4InitWriter returns a processor that prepends a pending init segment
// (ftyp+moov) to the first non-empty data chunk of a rotated segment.
func NewFmp4InitWriter(pendingInit []byte) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"fmp4-init-writer",
		&fmp4InitWriterProcessor{pendingInit: append([]byte(nil), pendingInit...)},
	)
}

func (p *fmp4InitWriterProcessor) Open(_ context.Context, _ *logrus.Entry) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = false
	return nil
}

func (p *fmp4InitWriterProcessor) Process(_ context.Context, _ *logrus.Entry, data []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(data) == 0 {
		return data, nil
	}
	if p.written || len(p.pendingInit) == 0 {
		return data, nil
	}
	p.written = true
	out := make([]byte, 0, len(p.pendingInit)+len(data))
	out = append(out, p.pendingInit...)
	out = append(out, data...)
	return out, nil
}

func (p *fmp4InitWriterProcessor) Close() error { return nil }
