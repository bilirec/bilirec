package processors

import (
	"bufio"
	"context"
	"os"
	"sync"
	"time"

	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/sirupsen/logrus"
)

type BufferedStreamWriterProcessor struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	bufferSize int
	writer     *bufio.Writer
	logger     *logrus.Entry

	ctx    context.Context
	cancel context.CancelFunc
	wait   sync.WaitGroup
}

func NewBufferedStreamWriter(path string, bufferSize int) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"buffered-writer",
		&BufferedStreamWriterProcessor{
			path:       path,
			bufferSize: bufferSize,
		},
		pipeline.WithTimeout[[]byte](30*time.Second),
	)
}

func (w *BufferedStreamWriterProcessor) Open(ctx context.Context, log *logrus.Entry) error {
	file, err := os.Create(w.path)
	if err != nil {
		return err
	}
	w.file = file
	w.writer = bufio.NewWriterSize(file, w.bufferSize)
	w.logger = log.WithField("file", file.Name())

	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.wait.Add(2)
	go w.flushPeriodically(w.ctx)
	go w.syncPeriodically(w.ctx)
	return nil
}

func (w *BufferedStreamWriterProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.writer.Write(data)
	return data, err
}

func (w *BufferedStreamWriterProcessor) Close() error {
	w.cancel()
	w.wait.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.writer.Flush(); err != nil {
		w.logger.Warnf("error flushing writer: %v", err)
	} else if err := w.file.Sync(); err != nil {
		w.logger.Warnf("error syncing file: %v", err)
	}
	return w.file.Close()
}

func (w *BufferedStreamWriterProcessor) flushPeriodically(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer w.wait.Done()
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			if err := w.writer.Flush(); err != nil {
				w.logger.Warnf("error flushing writer: %v", err)
			}
			w.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

func (w *BufferedStreamWriterProcessor) syncPeriodically(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer w.wait.Done()
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			if err := w.file.Sync(); err != nil {
				w.logger.Warnf("error syncing file: %v", err)
			}
			w.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}
