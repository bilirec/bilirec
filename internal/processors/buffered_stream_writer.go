package processors

import (
	"context"
	"hash/fnv"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/pkg/coordinator"
	"github.com/bilirec/bilirec/pkg/filecache"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/pkg/rw"
	"github.com/bilirec/bilirec/utils"
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

	defaultSkipSmallFlushThreshold = 1 * 1024 * 1024 // 1MB
	// maxPendingBytesBeforeDrop avoids unbounded in-memory growth when the output
	// file cannot be created for a long time (e.g., permission/disk/path issues).
	// Once pending buffered bytes exceed this hard limit, buffered chunks are dropped.
	maxPendingBytesBeforeDrop = 100 * 1024 * 1024 // 100MB
	// createFileFailureLogInterval limits warning frequency when file creation keeps failing.
	createFileFailureLogInterval = 5 * time.Second
)

type tryLocker interface {
	sync.Locker
	TryLock() bool
}

// noOpLocker is a dummy locker that does nothing. Used when sequential write is disabled.
type noOpLocker struct{}

func (n *noOpLocker) Lock()         {}
func (n *noOpLocker) Unlock()       {}
func (n *noOpLocker) TryLock() bool { return true }

type BufferedStreamWriterProcessor struct {
	file                    atomic.Pointer[os.File]
	path                    string
	bufferSize              int
	chanBufferSize          int
	syncPeriod              time.Duration
	flushPeriod             time.Duration
	sdcardProtection        bool
	skipSmallFlushThreshold int
	dropFilePageCache       bool
	sequentialWrite         bool
	writer                  *rw.FlushLockedBufferedWriter
	logger                  *logrus.Entry
	locker                  tryLocker

	dataCh       chan []byte
	stopCh       chan struct{} // Signal to stop syncWorker (only used if syncPeriod > 0)
	wait         sync.WaitGroup
	bytesWritten int64
	// bufferedBytes mirrors writer.Buffered() for cross-goroutine reads (e.g. periodic ready).
	bufferedBytes atomic.Int64

	// bytesPool is used to reduce allocations when copying incoming data.
	// When nil, fallback to direct allocation via make().
	bytesPool     *pool.BucketedBytesPool
	pendingChunks [][]byte
	pendingBytes  int

	lastCreateFileFailureLog time.Time
	suppressedCreateFailures int

	periodicSignalCh      <-chan struct{}
	periodicUnregister    func()
	minPeriodicFlushBytes int
}

// livePeriodicRoundRobin serializes periodic work across live writers when SEQUENTIAL_WRITE is on.
var livePeriodicRoundRobin = coordinator.NewRoundRobin(defaultFlushPeriod)

// globalMu is used to serialize flushes/syncs across multiple instances when sequentialWrite is enabled.
// in MicroSD card scenarios, concurrent flushes can cause significant performance degradation,
// so this global lock ensures that only one flush/write operation happens at a time across all BufferedStreamWriterProcessor instances.
var globalMu sync.Mutex

type BufferedStreamWriterOptions = func(*BufferedStreamWriterProcessor)

func NewBufferedStreamWriter(path string, opts ...BufferedStreamWriterOptions) *pipeline.ProcessorInfo[[]byte] {
	processor := &BufferedStreamWriterProcessor{
		path:                    path,
		bufferSize:              1 * 1024 * 1024, // default 1MB
		chanBufferSize:          defaultChanBufferSize,
		syncPeriod:              45 * time.Second,
		flushPeriod:             defaultFlushPeriod,
		sdcardProtection:        false,
		skipSmallFlushThreshold: defaultSkipSmallFlushThreshold,
		sequentialWrite:         false,
	}
	processor.applyOptions(opts...)
	if processor.skipSmallFlushThreshold <= 0 {
		processor.skipSmallFlushThreshold = defaultSkipSmallFlushThreshold
	}
	if processor.bytesPool == nil {
		processor.bytesPool = pool.NewBucketedBytesPool(defaultChanBufferSize * 1024)
	}
	if processor.minPeriodicFlushBytes <= 0 {
		processor.minPeriodicFlushBytes = max(processor.bufferSize / 4, 64 * 1024)
	}
	processor.locker = utils.TernaryFunc(
		processor.sequentialWrite,
		func() tryLocker { return &globalMu },
		func() tryLocker { return &noOpLocker{} },
	)
	return pipeline.NewProcessorInfo(
		"buffered-writer",
		processor,
		pipeline.WithTimeout[[]byte](30*time.Second),
	)
}

func (w *BufferedStreamWriterProcessor) Open(ctx context.Context, log *logrus.Entry) error {
	// File creation is delayed when SD card protection is enabled. We keep
	// small writes in memory until the total queued bytes reach skipSmallFlushThreshold.
	if !w.sdcardProtection {
		file, err := os.Create(w.path)
		if err != nil {
			return err
		}
		w.file.Store(file)
		w.writer = rw.NewFlushLockedBufferedWriter(file, w.bufferSize, w.locker)
	} else {
		w.pendingChunks = make([][]byte, 0, 16)
		w.pendingBytes = 0
	}

	w.logger = log.WithField("file", w.path)
	w.dataCh = make(chan []byte, w.chanBufferSize)

	if w.sequentialWrite {
		livePeriodicRoundRobin.SetCyclePeriod(w.flushPeriod)
		ch, unreg := livePeriodicRoundRobin.Register(func() bool {
			return w.bufferedBytes.Load() >= int64(w.minPeriodicFlushBytes)
		})
		w.periodicSignalCh = ch
		w.periodicUnregister = unreg
	}

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
	if len(data) == 0 {
		return data, nil
	}

	cp := w.bytesPool.GetSized(len(data))
	copy(cp, data)

	select {
	case w.dataCh <- cp:
	case <-ctx.Done():
		w.bytesPool.Put(cp)
		return data, ctx.Err()
	}
	return data, nil
}

func (w *BufferedStreamWriterProcessor) Close() error {
	if w.periodicUnregister != nil {
		w.periodicUnregister()
		w.periodicUnregister = nil
		w.periodicSignalCh = nil
	}
	close(w.dataCh) // Producer closes the channel
	if w.syncPeriod > 0 {
		close(w.stopCh) // Signal syncWorker to stop (if it's running)
	}
	w.wait.Wait()

	if w.sdcardProtection && w.file.Load() == nil {
		threshold := w.skipSmallFlushThreshold
		switch {
		case w.pendingBytes == 0:
			return nil
		case w.pendingBytes < threshold:
			w.logger.Warnf("未创建文件且总写入字节数（%d）小于保护阈值（%d），直接丢弃内存中的数据", w.pendingBytes, threshold)
			w.releasePendingChunks()
			return nil
		default:
			if err := w.createFileAndWriter(); err != nil {
				w.logger.Warnf(
					"关闭时最后尝试创建文件失败，丢弃待写数据：pending=%dB threshold=%dB err=%v",
					w.pendingBytes,
					threshold,
					err,
				)
				w.releasePendingChunks()
				return nil
			}
			w.writePendingChunksToWriter()
		}
	}

	if w.writer != nil {
		if err := w.writer.Flush(); err != nil {
			w.logger.Warnf("刷新写入器失败：%v", err)
		} else {
			w.syncBufferedBytes()
			if file := w.file.Load(); file != nil {
				w.locker.Lock()
				if err := file.Sync(); err != nil {
					w.logger.Warnf("同步文件失败：%v", err)
				}
				w.locker.Unlock()
			}
		}
	}

	var closeErr error
	if file := w.file.Load(); file != nil {
		w.logger.Debugf("file path: %s, total written %vB", w.path, w.bytesWritten)
		if w.dropFilePageCache {
			w.locker.Lock()
			filecache.DropOpenFileCache(file)
			w.locker.Unlock()
		}
		closeErr = file.Close()
	}
	w.writer = nil
	w.file.Store(nil)
	return closeErr
}

// writePeriodically is the single writer goroutine. It owns all access to w.writer
// and w.file reads, decoupling TCP receive from file I/O.
// When dataCh is closed by Close(), it drains any remaining queued data before returning
// so Close can perform a final flush+sync safely.
func (w *BufferedStreamWriterProcessor) writePeriodically() {
	defer w.wait.Done()
	if w.sequentialWrite && w.periodicSignalCh != nil {
		w.writePeriodicallyCoordinated()
		return
	}
	w.writePeriodicallyWithTimer()
}

func (w *BufferedStreamWriterProcessor) writePeriodicallyCoordinated() {
	for {
		select {
		case data, ok := <-w.dataCh:
			if !ok {
				return
			}
			w.handleDataChunk(data)
		case <-w.periodicSignalCh:
			w.doPeriodicFlush()
		}
	}
}

func (w *BufferedStreamWriterProcessor) writePeriodicallyWithTimer() {
	flushTimer := time.NewTimer(w.flushPeriod + w.flushJitter())
	defer flushTimer.Stop()
	for {
		select {
		case data, ok := <-w.dataCh:
			if !ok {
				return
			}
			w.handleDataChunk(data)
		case <-flushTimer.C:
			w.doPeriodicFlush()
			flushTimer.Reset(w.flushPeriod)
		}
	}
}

func (w *BufferedStreamWriterProcessor) handleDataChunk(data []byte) {
	if w.sdcardProtection && w.file.Load() == nil {
		threshold := w.skipSmallFlushThreshold
		w.pendingChunks = append(w.pendingChunks, data)
		w.pendingBytes += len(data)
		if w.pendingBytes < threshold {
			return
		}
		// Buffer threshold reached; create file and flush pending data.
		if err := w.createFileAndWriter(); err != nil {
			if w.pendingBytes <= maxPendingBytesBeforeDrop {
				if w.shouldLogCreateFileFailure() {
					w.logger.Warnf(
						"创建文件失败，暂不丢弃待写数据：pending=%dB chunks=%d limit=%dB suppressed=%d err=%v",
						w.pendingBytes,
						len(w.pendingChunks),
						maxPendingBytesBeforeDrop,
						w.suppressedCreateFailures,
						err,
					)
					w.suppressedCreateFailures = 0
				} else {
					w.suppressedCreateFailures++
				}
				return
			}
			if w.suppressedCreateFailures > 0 {
				w.logger.Warnf("创建文件失败日志节流期间累计 suppressed=%d", w.suppressedCreateFailures)
				w.suppressedCreateFailures = 0
			}
			w.logger.Warnf(
				"创建文件失败且待写数据超过硬阈值，丢弃待写数据以防止内存持续增长：dropped=%dB chunks=%d limit=%dB err=%v",
				w.pendingBytes,
				len(w.pendingChunks),
				maxPendingBytesBeforeDrop,
				err,
			)
			w.releasePendingChunks()
			return
		}
		// Reset throttling state after file creation recovers.
		w.lastCreateFileFailureLog = time.Time{}
		w.suppressedCreateFailures = 0
		w.writePendingChunksToWriter()
		return
	}

	if w.writer != nil {
		n, err := w.writer.Write(data)
		if n != len(data) {
			w.logger.Warnf("写入数据长度不完整：%d/%d", n, len(data))
		}
		if err != nil {
			w.logger.Warnf("写入数据失败：%v", err)
		}
		w.bytesWritten += int64(n)
		w.syncBufferedBytes()
		w.bytesPool.Put(data)
	}
}

func (w *BufferedStreamWriterProcessor) syncBufferedBytes() {
	if w.writer == nil {
		w.bufferedBytes.Store(0)
		return
	}
	w.bufferedBytes.Store(int64(w.writer.Buffered()))
}

func (w *BufferedStreamWriterProcessor) doPeriodicFlush() {
	if w.writer == nil {
		return
	}
	if w.writer.Buffered() < w.minPeriodicFlushBytes {
		return
	}
	flushStart := time.Now()
	if err := w.writer.Flush(); err != nil {
		w.logger.Warnf("刷新写入器失败：%v", err)
		return
	}
	w.syncBufferedBytes()
	if flushCost := time.Since(flushStart); flushCost > defaultSlowFlushWarnThreshold {
		w.logger.Warnf("周期性 flush 较慢：耗时=%s", flushCost)
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

func (w *BufferedStreamWriterProcessor) createFileAndWriter() error {
	if w.file.Load() != nil {
		return nil
	}

	file, err := os.Create(w.path)
	if err != nil {
		return err
	}
	w.file.Store(file)
	w.writer = rw.NewFlushLockedBufferedWriter(file, w.bufferSize, w.locker)
	return nil
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
			file := w.file.Load()
			if file == nil {
				continue
			}
			// Perform the actual sync to disk (can be slow on SD cards).
			syncStart := time.Now()
			if !w.locker.TryLock() {
				continue
			}
			if err := file.Sync(); err != nil {
				w.logger.Warnf("同步文件失败：%v", err)
			}
			w.locker.Unlock()
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

func WithDropFilePageCache(enabled bool) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.dropFilePageCache = enabled
	}
}

func WithSkipSmallFlushThreshold(threshold int) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.skipSmallFlushThreshold = threshold
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

// WithBytesPool configures a bucketed bytes pool to reuse buffers during copying,
// reducing GC pressure. Buckets are typically computed from
// LIVE_STREAM_WRITER_BYTES_POOL_SIZE and include several nearby sizes.
//
//	pool := pool.NewBucketedBytesPool(512 * 1024)
//	writer := NewBufferedStreamWriter(path, WithBytesPool(pool))
func WithBytesPool(bp *pool.BucketedBytesPool) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.bytesPool = bp
	}
}

func (w *BufferedStreamWriterProcessor) releasePendingChunks() {
	for _, chunk := range w.pendingChunks {
		w.bytesPool.Put(chunk)
	}
	w.pendingChunks = nil
	w.pendingBytes = 0
}

func (w *BufferedStreamWriterProcessor) writePendingChunksToWriter() {
	for _, chunk := range w.pendingChunks {
		n, err := w.writer.Write(chunk)
		if n != len(chunk) {
			w.logger.Warnf("写入待缓存数据长度不完整：%d/%d", n, len(chunk))
		}
		if err != nil {
			w.logger.Warnf("写入待缓存数据失败：%v", err)
		}
		w.bytesWritten += int64(n)
		w.bytesPool.Put(chunk)
	}
	w.pendingChunks = w.pendingChunks[:0]
	w.pendingBytes = 0
	w.syncBufferedBytes()
}

func (w *BufferedStreamWriterProcessor) shouldLogCreateFileFailure() bool {
	now := time.Now()
	if w.lastCreateFileFailureLog.IsZero() || now.Sub(w.lastCreateFileFailureLog) >= createFileFailureLogInterval {
		w.lastCreateFileFailureLog = now
		return true
	}
	return false
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

// WithMinPeriodicFlushBytes sets the minimum buffered bytes required before a
// coordinated periodic flush runs. Defaults to bufferSize/4 (minimum 64KB).
func WithMinPeriodicFlushBytes(n int) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		if n > 0 {
			p.minPeriodicFlushBytes = n
		}
	}
}
