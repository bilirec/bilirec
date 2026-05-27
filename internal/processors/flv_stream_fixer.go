package processors

import (
	"context"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
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
	p.fixer.SetTimestampJumpReporter(func(w flv.TimestampJumpWarning) {
		p.log.Warnf(
			"检测到 FLV 时间戳跳变：current=%dms previous=%dms delta=%dms offset=%d->%d rotation=%v tagType=0x%02x",
			w.CurrentTimestamp,
			w.PreviousTimestamp,
			w.Delta,
			w.PreviousOffset,
			w.AppliedOffset,
			w.IsRotationSegment,
			w.TagType,
		)
	})
	return nil
}

func (p *FlvStreamFixerProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	return p.fixer.Fix(data)
}

func (p *FlvStreamFixerProcessor) Close() error {
	dups, size, capacity := p.fixer.GetDedupStats()
	p.log.Infof("🗂️ 去重统计：检测到 %d 个重复片段，缓存大小：%d/%d", dups, size, capacity)
	if p.own {
		p.fixer.Close()
	}
	return nil
}
