package processors

import (
	"context"
	"hash/fnv"
	"os"
	"sync"
	"time"

	"github.com/eric2788/bilirec/pkg/pipeline"
	"github.com/eric2788/bilirec/pkg/pool"
	"github.com/eric2788/bilirec/pkg/rw"
	"github.com/eric2788/bilirec/utils"
	"github.com/sirupsen/logrus"
)

const (
	// defaultSlowFlushWarnThreshold warns when buffered flush takes too long.
	// 3s is more suitable for microSD cards under concurrent I/O pressure.
	defaultSlowFlushWarnThreshold = 3 * time.Second

	// slowSyncWarnThreshold warns when fsync takes too long.
	// This is only used if periodic sync is enabled (syncPeriod > 0).
	// For microSD: warns if fsync > 1200ms (card is struggling).
	// For HDD: consider lowering to 200ms (HDDs should sync very fast).
	slowSyncWarnThreshold = 1200 * time.Millisecond

	// Default number of data chunks that can be queued before Process blocks.
	defaultChanBufferSize = 256

	// defaultFlushPeriod controls how often buffered data is flushed to the OS.
	// 15s reduces flush frequency to lower SD card wear.
	defaultFlushPeriod = 15 * time.Second
)

// noOpLocker is a dummy locker that does nothing. Used when sequential write is disabled.
type noOpLocker struct{}

func (n *noOpLocker) Lock()   {}
func (n *noOpLocker) Unlock() {}

type BufferedStreamWriterProcessor struct {
	file             *os.File
	path             string
	bufferSize       int
	chanBufferSize   int
	syncPeriod       time.Duration
	flushPeriod      time.Duration
	sdcardProtection bool
	sequentialWrite  bool
	writer           *rw.FlushLockedBufferedWriter
	logger           *logrus.Entry

	dataCh       chan []byte
	stopCh       chan struct{} // Signal to stop syncWorker (only used if syncPeriod > 0)
	wait         sync.WaitGroup
	bytesWritten int64

	// bytesPool is used to reduce allocations when copying incoming data.
	// When nil, fallback to direct allocation via make().
	bytesPool *pool.BytesPool
}

// globalFlushMu is used to serialize flushes across multiple instances when sequentialWrite is enabled.
// in MicroSD card scenarios, concurrent flushes can cause significant performance degradation,
// so this global lock ensures that only one flush/write operation happens at a time across all BufferedStreamWriterProcessor instances.
var globalFlushMu sync.Mutex

type BufferedStreamWriterOptions = func(*BufferedStreamWriterProcessor)

func NewBufferedStreamWriter(path string, opts ...BufferedStreamWriterOptions) *pipeline.ProcessorInfo[[]byte] {
	processor := &BufferedStreamWriterProcessor{
		path:             path,
		bufferSize:       1 * 1024 * 1024, // default 1MB
		chanBufferSize:   defaultChanBufferSize,
		syncPeriod:       45 * time.Second,
		flushPeriod:      defaultFlushPeriod,
		sdcardProtection: false,
		sequentialWrite:  false,
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

	flushLocker := utils.TernaryFunc(
		w.sequentialWrite,
		func() sync.Locker { return &globalFlushMu },
		func() sync.Locker { return &noOpLocker{} },
	)

	w.writer = rw.NewFlushLockedBufferedWriter(file, w.bufferSize, flushLocker)
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
		w.logger.Warnf("已写入总字节数（%d）小于缓冲区大小（%d），为减少 SD 卡磨损跳过 flush", w.bytesWritten, w.bufferSize)
		return w.file.Close()
	}
	if err := w.writer.Flush(); err != nil {
		w.logger.Warnf("刷新写入器失败：%v", err)
	} else if err := w.file.Sync(); err != nil {
		w.logger.Warnf("同步文件失败：%v", err)
	}
	w.logger.Debugf("file path: %s, total written %vB", w.path, w.bytesWritten)
	return w.file.Close()
}

// writePeriodically is the single writer goroutine. It owns all access to w.writer
// and w.file reads, decoupling TCP receive from file I/O.
// When dataCh is closed by Close(), it drains any remaining queued data before returning
// so Close can perform a final flush+sync safely.
func (w *BufferedStreamWriterProcessor) writePeriodically() {
	flushTimer := time.NewTimer(w.flushPeriod + w.flushJitter())
	defer w.wait.Done()
	defer flushTimer.Stop()

	for {
		select {
		case data, ok := <-w.dataCh:
			if !ok {
				return
			}
			n, err := w.writer.Write(data)
			w.bytesWritten += int64(n)
			if err != nil {
				w.logger.Warnf("写入数据失败：%v", err)
			}
			// Return buffer to pool after writing (if pool is enabled).
			if w.bytesPool != nil {
				w.bytesPool.PutBytes(data)
			}
		case <-flushTimer.C:
			flushStart := time.Now()
			if err := w.writer.Flush(); err != nil {
				w.logger.Warnf("刷新写入器失败：%v", err)
			}
			if flushCost := time.Since(flushStart); flushCost > defaultSlowFlushWarnThreshold {
				w.logger.Warnf("周期性 flush 较慢：耗时=%s", flushCost)
			}
			flushTimer.Reset(w.flushPeriod)
		}
	}
}

// flushJitter returns a deterministic per-file flush phase offset to reduce
// synchronized flush bursts when multiple streams start around the same time.
func (w *BufferedStreamWriterProcessor) flushJitter() time.Duration {
	if w.flushPeriod <= 1*time.Second {
		return 0
	}
	maxJitter := w.flushPeriod / 2
	if maxJitter <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(w.path))
	return time.Duration(h.Sum64() % uint64(maxJitter))
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
				w.logger.Warnf("同步文件失败：%v", err)
			}
			if syncCost := time.Since(syncStart); syncCost > slowSyncWarnThreshold {
				w.logger.Warnf("周期性 sync 较慢：耗时=%s", syncCost)
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

// WithFlushPeriod sets how often the buffered writer flushes data to the OS.
// Larger periods reduce flush frequency and SD card wear.
func WithFlushPeriod(period time.Duration) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		if period > 0 {
			p.flushPeriod = period
		}
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

// WithSequentialWrite enables global flush locking to serialize all flush/write
// operations across multiple BufferedStreamWriterProcessor instances. This is useful
// when multiple streams write to the same physical disk and you want to prevent
// simultaneous flush bursts from causing I/O peaks. Default is disabled (no locking).
func WithSequentialWrite(enabled bool) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.sequentialWrite = enabled
	}
}
