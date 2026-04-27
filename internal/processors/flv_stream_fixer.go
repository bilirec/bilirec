package processors

import (
	"context"

	"github.com/eric2788/bilirec/pkg/flv"
	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

var ErrNotFlvFile = flv.ErrNotFlvFile

type FlvStreamFixerProcessor struct {
	fixer *flv.RealtimeFixer
	log   *logrus.Entry
	own   bool
}

func NewFlvStreamFixer() *pipeline.ProcessorInfo[[]byte] {
	ffp := &FlvStreamFixerProcessor{
		fixer: flv.NewRealtimeFixer(),
		own:   true,
	}
	return pipeline.NewProcessorInfo(
		"flv-fixer",
		ffp,
	)
}

// NewFlvStreamFixerWithFixer reuses an external RealtimeFixer instance.
// The processor will not close/reset this fixer in Close().
func NewFlvStreamFixerWithFixer(fixer *flv.RealtimeFixer) *pipeline.ProcessorInfo[[]byte] {
	ffp := &FlvStreamFixerProcessor{
		fixer: fixer,
		own:   false,
	}
	return pipeline.NewProcessorInfo(
		"flv-fixer",
		ffp,
	)
}

func (p *FlvStreamFixerProcessor) Open(ctx context.Context, log *logrus.Entry) error {
	p.log = log
	return nil
}

func (p *FlvStreamFixerProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	return p.fixer.Fix(data)
}

func (p *FlvStreamFixerProcessor) Close() error {
	dups, size, capacity := p.fixer.GetDedupStats()
	p.log.Infof("🗂️ Dedup Stats: %d duplicates detected, cache size: %d/%d", dups, size, capacity)
	if p.own {
		p.fixer.Close()
	}
	return nil
}
