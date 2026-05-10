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

const (
	// Warn only when periodic flush/sync is abnormally slow to avoid noisy logs.
	slowFlushWarnThreshold = 100 * time.Millisecond
	slowSyncWarnThreshold  = 500 * time.Millisecond
)

type BufferedStreamWriterProcessor struct {
	mu               sync.Mutex
	file             *os.File
	path             string
	bufferSize       int
	sdcardProtection bool
	writer           *bufio.Writer
	logger           *logrus.Entry

	ctx          context.Context
	cancel       context.CancelFunc
	wait         sync.WaitGroup
	bytesWritten int64
}

type BufferedStreamWriterOptions = func(*BufferedStreamWriterProcessor)

func NewBufferedStreamWriter(path string, opts ...BufferedStreamWriterOptions) *pipeline.ProcessorInfo[[]byte] {
	processor := &BufferedStreamWriterProcessor{
		path:             path,
		bufferSize:       1 * 1024 * 1024, // default 1MB
		sdcardProtection: false,
	}
	processor.applyOptions(opts...)
	return pipeline.NewProcessorInfo(
		"buffered-writer",
		processor,
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
	w.wait.Add(1)
	go w.writePeriodically(w.ctx)
	return nil
}

func (w *BufferedStreamWriterProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.writer.Write(data)
	w.bytesWritten += int64(n)
	return data, err
}

func (w *BufferedStreamWriterProcessor) Close() error {
	w.cancel()
	w.wait.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sdcardProtection && w.bytesWritten < int64(w.bufferSize) {
		w.logger.Warnf("total bytes written (%d) less than buffer size (%d), skipping flush to reduce SD card wear", w.bytesWritten, w.bufferSize)
		return w.file.Close()
	}
	if err := w.writer.Flush(); err != nil {
		w.logger.Warnf("error flushing writer: %v", err)
	} else if err := w.file.Sync(); err != nil {
		w.logger.Warnf("error syncing file: %v", err)
	}
	w.logger.Debugf("file path: %s, total written %vB", w.path, w.bytesWritten)
	return w.file.Close()
}

func (w *BufferedStreamWriterProcessor) writePeriodically(ctx context.Context) {
	flushTicker := time.NewTicker(5 * time.Second)
	syncTicker := time.NewTicker(30 * time.Second)
	defer w.wait.Done()
	defer flushTicker.Stop()
	defer syncTicker.Stop()
	for {
		select {
		case <-flushTicker.C:
			w.mu.Lock()
			flushStart := time.Now()
			if err := w.writer.Flush(); err != nil {
				w.logger.Warnf("error flushing writer: %v", err)
			}
			if flushCost := time.Since(flushStart); flushCost > slowFlushWarnThreshold {
				w.logger.Warnf("slow periodic flush: cost=%s", flushCost)
			}
			w.mu.Unlock()
		case <-syncTicker.C:
			w.mu.Lock()
			flushStart := time.Now()
			if err := w.writer.Flush(); err != nil {
				w.logger.Warnf("error flushing writer: %v", err)
			}
			if flushCost := time.Since(flushStart); flushCost > slowFlushWarnThreshold {
				w.logger.Warnf("slow pre-sync flush: cost=%s", flushCost)
			}
			w.mu.Unlock()
			syncStart := time.Now()
			if err := w.file.Sync(); err != nil {
				w.logger.Warnf("error syncing file: %v", err)
			}
			if syncCost := time.Since(syncStart); syncCost > slowSyncWarnThreshold {
				w.logger.Warnf("slow periodic sync: cost=%s", syncCost)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (p *BufferedStreamWriterProcessor) applyOptions(opts ...BufferedStreamWriterOptions) {
	for _, opt := range opts {
		opt(p)
	}
}

func WithSDCardProtection(enabled bool) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.sdcardProtection = enabled
	}
}

func WithBufferSize(size int) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.bufferSize = size
	}
}
