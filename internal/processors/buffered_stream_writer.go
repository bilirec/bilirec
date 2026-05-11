package processors

import (
	"bufio"
	"context"
	"os"
	"sync"
	"time"

	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/eric2788/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

const (
	// slowFlushWarnThreshold warns when buffered flush takes too long.
	// For microSD cards: 500ms is appropriate (cards can be slow).
	// For HDDs: if you enable periodic sync, consider lowering this to 100ms
	// (HDDs should sync fast; anything slower indicates I/O pressure).
	slowFlushWarnThreshold = 500 * time.Millisecond

	// slowSyncWarnThreshold warns when fsync takes too long.
	// This is only used if periodic sync is enabled (syncPeriod > 0).
	// For microSD: warns if fsync > 800ms (card is struggling).
	// For HDD: consider lowering to 200ms (HDDs should sync very fast).
	slowSyncWarnThreshold = 800 * time.Millisecond

	// Default number of data chunks that can be queued before Process blocks.
	defaultChanBufferSize = 256
)

type BufferedStreamWriterProcessor struct {
	file             *os.File
	path             string
	bufferSize       int
	chanBufferSize   int
	syncPeriod       time.Duration
	sdcardProtection bool
	writer           *bufio.Writer
	logger           *logrus.Entry

	dataCh       chan []byte
	stopCh       chan struct{} // Signal to stop syncWorker (only used if syncPeriod > 0)
	wait         sync.WaitGroup
	bytesWritten int64

	// bytesPool is used to reduce allocations when copying incoming data.
	// When nil, fallback to direct allocation via make().
	bytesPool *pool.BytesPool
}

type BufferedStreamWriterOptions = func(*BufferedStreamWriterProcessor)

func NewBufferedStreamWriter(path string, opts ...BufferedStreamWriterOptions) *pipeline.ProcessorInfo[[]byte] {
	processor := &BufferedStreamWriterProcessor{
		path:             path,
		bufferSize:       1 * 1024 * 1024, // default 1MB
		chanBufferSize:   defaultChanBufferSize,
		syncPeriod:       45 * time.Second,
		sdcardProtection: false,
	}
	processor.applyOptions(opts...)
	if processor.bytesPool == nil {
		processor.bytesPool = pool.NewBytesPool(defaultChanBufferSize * 1024) // default pool buffers to match chan buffer size
	}
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
	w.dataCh = make(chan []byte, w.chanBufferSize)

	// Start the periodic writer goroutine.
	w.wait.Add(1)
	go w.writePeriodically()

	// If periodic sync is enabled, start the sync worker goroutine.
	if w.syncPeriod > 0 {
		w.stopCh = make(chan struct{})
		w.wait.Add(1)
		go w.syncWorker()
	}

	return nil
}

// Process copies data into the write goroutine's channel and immediately returns,
// decoupling TCP receive from file I/O to prevent backpressure-induced frame drops.
func (w *BufferedStreamWriterProcessor) Process(ctx context.Context, log *logrus.Entry, data []byte) ([]byte, error) {
	var cp []byte
	// Try to get a pre-allocated buffer from the pool.
	pooledBuf := w.bytesPool.GetBytes()
	if cap(pooledBuf) >= len(data) {
		cp = pooledBuf[:len(data)]
		copy(cp, data)
	} else {
		// Buffer too small; put it back and allocate.
		w.bytesPool.PutBytes(pooledBuf)
		cp = make([]byte, len(data))
		copy(cp, data)
	}
	select {
	case w.dataCh <- cp:
	case <-ctx.Done():
		w.bytesPool.PutBytes(cp)
		return data, ctx.Err()
	}
	return data, nil
}

func (w *BufferedStreamWriterProcessor) Close() error {
	close(w.dataCh) // Producer closes the channel
	if w.syncPeriod > 0 {
		close(w.stopCh) // Signal syncWorker to stop (if it's running)
	}
	w.wait.Wait()
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

// writePeriodically is the single writer goroutine. It owns all access to w.writer
// and w.file reads, decoupling TCP receive from file I/O.
// When dataCh is closed by Close(), it drains any remaining queued data before returning
// so Close can perform a final flush+sync safely.
func (w *BufferedStreamWriterProcessor) writePeriodically() {
	flushTicker := time.NewTicker(5 * time.Second)
	defer w.wait.Done()
	defer flushTicker.Stop()

	for {
		select {
		case data, ok := <-w.dataCh:
			if !ok {
				// Channel is closed; drain any remaining data and exit.
				// This is important because Close() closes dataCh after canceling context.
				for data := range w.dataCh {
					n, err := w.writer.Write(data)
					w.bytesWritten += int64(n)
					if err != nil {
						w.logger.Warnf("error writing remaining data: %v", err)
					}
					w.bytesPool.PutBytes(data)
				}
				return
			}
			n, err := w.writer.Write(data)
			w.bytesWritten += int64(n)
			if err != nil {
				w.logger.Warnf("error writing data: %v", err)
			}
			// Return buffer to pool after writing (if pool is enabled).
			if w.bytesPool != nil {
				w.bytesPool.PutBytes(data)
			}
		case <-flushTicker.C:
			flushStart := time.Now()
			if err := w.writer.Flush(); err != nil {
				w.logger.Warnf("error flushing writer: %v", err)
			}
			if flushCost := time.Since(flushStart); flushCost > slowFlushWarnThreshold {
				w.logger.Warnf("slow periodic flush: cost=%s", flushCost)
			}
		}
	}
}

// syncWorker handles periodic fsync operations in a separate goroutine to avoid
// blocking the main writer loop on slow I/O (especially important for SD cards).
// It owns a ticker and independently drives sync operations at w.syncPeriod intervals
// until stopCh is closed.
func (w *BufferedStreamWriterProcessor) syncWorker() {
	syncTicker := time.NewTicker(w.syncPeriod)
	defer w.wait.Done()
	defer syncTicker.Stop()

	for {
		select {
		case <-syncTicker.C:
			// Perform the actual sync to disk (can be slow on SD cards).
			syncStart := time.Now()
			if err := w.file.Sync(); err != nil {
				w.logger.Warnf("error syncing file: %v", err)
			}
			if syncCost := time.Since(syncStart); syncCost > slowSyncWarnThreshold {
				w.logger.Warnf("slow periodic sync: cost=%s", syncCost)
			}
		case <-w.stopCh:
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

// WithSyncPeriod sets the periodic fsync interval. Pass 0 to disable periodic
// sync entirely (sync only on Close), which reduces wear on SD cards.
func WithSyncPeriod(period time.Duration) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.syncPeriod = period
	}
}

// WithChanBufferSize sets the number of data chunks that can be queued in the
// write channel before Process blocks. Larger values trade memory for more
// tolerance to bursty write latency.
func WithChanBufferSize(size int) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		if size > 0 {
			p.chanBufferSize = size
		}
	}
}

// WithBytesPool configures a pool.BytesPool to reuse buffers during copying,
// reducing GC pressure. Pool should be configured for a fixed buffer size
// that matches your typical chunk size. Example:
//
//	pool := pool.NewBytesPool(256 * 1024) // 256KB chunks
//	writer := NewBufferedStreamWriter(path, WithBytesPool(pool))
//
// If incoming data is larger than the pool's configured size, the processor will
// allocate a fresh buffer; oversized buffers are not returned to the pool (see BytesPool.PutBytes).
func WithBytesPool(bp *pool.BytesPool) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.bytesPool = bp
	}
}
