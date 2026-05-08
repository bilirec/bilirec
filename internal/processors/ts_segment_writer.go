package processors

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

// TsSegmentWriterProcessor appends complete HLS MPEG-TS segments to a single
// .ts file. Each Process() call receives one full segment (not a 256KB chunk).
// The bytes are already valid MPEG-TS from Bilibili — no re-encoding is done.
type TsSegmentWriterProcessor struct {
	mu     sync.Mutex
	path   string
	file   *os.File
	logger *logrus.Entry
	syncer *periodicFileSync
}

func NewTsSegmentWriter(path string) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"ts-segment-writer",
		&TsSegmentWriterProcessor{path: path},
	)
}

func (p *TsSegmentWriterProcessor) Open(ctx context.Context, log *logrus.Entry) error {
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	p.file = f
	p.logger = log.WithField("file", f.Name())
	p.syncer = startPeriodicFileSync(&p.mu, p.file, p.logger, 30*time.Second)
	return nil
}

func (p *TsSegmentWriterProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.file.Write(data)
	return data, err
}

func (p *TsSegmentWriterProcessor) Close() error {
	p.syncer.Stop()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.file.Sync(); err != nil {
		p.logger.Warnf("error syncing file: %v", err)
	}
	return p.file.Close()
}
