package record_strategies

import (
	"context"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
)

// RotationState carries format-specific state across segment rotations.
// Each strategy stores and reads its own keys from Data.
type RotationState struct {
	Data map[string][]byte
}

type ErrAction uint8

const (
	// ErrActionAbort stops recording gracefully (optionally after AbortDelay).
	ErrActionAbort ErrAction = iota
	// ErrActionRotate starts the next segment using State.
	ErrActionRotate
)

type ErrHandleResult struct {
	Action ErrAction
	State  *RotationState

	// AbortDelay is only used when Action == ErrActionAbort.
	AbortDelay time.Duration
}

// StreamRecordStrategy encapsulates format-specific pipeline construction
// and rotation logic, keeping rotate() format-agnostic.
//
// Lifecycle per recording session:
//
//  1. BuildPipeline(ctx, outputPath, state) — called at the start of each segment.
//     For segment 0, state.Data is empty. For subsequent segments, state contains
//     whatever a previous HandleErr(Action=Rotate) returned.
//
//  2. HandleErr(err) — called when the pipeline returns an error.
//     Returns ErrActionRotate to continue with a new segment.
//     Returns ErrActionAbort to stop gracefully.
//
//  3. Close() — called once when the recording session ends (deferred in rotate()).
type StreamRecordStrategy interface {
	// FileExtension returns the output file extension including the leading dot,
	// e.g. ".flv", ".ts", ".mp4".
	FileExtension() string
	BuildPipeline(ctx context.Context, outputPath string, state *RotationState) (*pipeline.Pipe[[]byte], error)
	HandleErr(err error) ErrHandleResult
	Close() error
}

var (
	writerPoolOnce sync.Once
	writerPools    *pool.LazyDualPool[*pool.BucketedBytesPool]

	parsePoolOnce sync.Once
	parsePools    *pool.LazyDualPool[*pool.BufferPool]
)

func newWriterPool(size int) *pool.BucketedBytesPool {
	return pool.NewBucketedBytesPool(size)
}

func newParseBufferPool(size int) *pool.BufferPool {
	return pool.NewBufferPool(
		size,
		size,
		pool.WithBoundedMode(true),
		pool.WithBoundedCapacity(2),
	)
}

func getWriterPools() *pool.LazyDualPool[*pool.BucketedBytesPool] {
	writerPoolOnce.Do(func() {
		writerPools = pool.NewLazyDualPool(
			15*time.Minute,
			func() *pool.BucketedBytesPool { return newWriterPool(config.ReadOnly.LiveStreamWriterBytesPoolSize()) },
			func() *pool.BucketedBytesPool { return newWriterPool(config.ReadOnly.LiveStreamWriterBytesPoolSizeHigh()) },
		)
	})
	return writerPools
}

func getParseBufferPools() *pool.LazyDualPool[*pool.BufferPool] {
	parsePoolOnce.Do(func() {
		parsePools = pool.NewLazyDualPool(
			15*time.Minute,
			func() *pool.BufferPool { return newParseBufferPool(config.ReadOnly.ReadStreamBytesPoolSize()) },
			func() *pool.BufferPool { return newParseBufferPool(config.ReadOnly.ReadStreamBytesPoolSizeHigh()) },
		)
	})
	return parsePools
}

func acquireWriterPool(qn int) (*pool.BucketedBytesPool, func()) {
	return getWriterPools().Acquire(config.IsHighQualityQn(qn))
}

func acquireParseBufferPool(qn int) (*pool.BufferPool, func()) {
	return getParseBufferPools().Acquire(config.IsHighQualityQn(qn))
}
