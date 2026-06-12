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
	writerPools    *pool.LazyDualPool[pool.ByteSlicePool]
)

func newWriterPool(size int) pool.ByteSlicePool {
	if config.ReadOnly.IsLowMemPreset() {
		// low-mem keeps a single-cap pool to reduce bucketed long-tail retention.
		return pool.NewBytesSlicePool(size, size)
	}
	return pool.NewBucketedBytesPool(size)
}

func getWriterPools() *pool.LazyDualPool[pool.ByteSlicePool] {
	writerPoolOnce.Do(func() {
		writerPools = pool.NewLazyDualPool(
			15*time.Minute,
			func() pool.ByteSlicePool { return newWriterPool(config.ReadOnly.LiveStreamWriterBytesPoolSize()) },
			func() pool.ByteSlicePool { return newWriterPool(config.ReadOnly.LiveStreamWriterBytesPoolSizeHigh()) },
		)
	})
	return writerPools
}

func acquireWriterPool(qn int) (pool.ByteSlicePool, func()) {
	return getWriterPools().Acquire(config.IsHighQualityQn(qn))
}
