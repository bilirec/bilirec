package pool_test

import (
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/pool"
)

func TestAcquireReadPool_UsesDefaultAndHigh(t *testing.T) {
	defaultPool := pool.NewBytesPool(512 * 1024)
	lp := pool.NewLazyDualPool(
		15*time.Minute,
		func() *pool.BytesPool { return defaultPool },
		func() *pool.BytesPool { return pool.NewBytesPool(1024 * 1024) },
	)

	gotDefault, releaseDefault := lp.Acquire(false)
	if gotDefault != defaultPool {
		t.Fatal("expected default quality to use default pool")
	}
	releaseDefault()

	highPool, releaseHigh := lp.Acquire(true)
	if highPool == nil {
		t.Fatal("expected high quality to initialize high pool")
	}
	if highPool == defaultPool {
		t.Fatal("expected high quality to use dedicated high pool")
	}
	if lp.MaybeHigh() == nil {
		t.Fatal("expected high pool snapshot to be non-nil after acquire")
	}
	releaseHigh()
}

func TestTryCleanupHighPool_ReleasesWhenIdleAndNoRef(t *testing.T) {
	defaultPool := pool.NewBytesPool(512 * 1024)
	lp := pool.NewLazyDualPool(
		1*time.Minute,
		func() *pool.BytesPool { return defaultPool },
		func() *pool.BytesPool { return pool.NewBytesPool(1024 * 1024) },
	)

	_, releaseHigh := lp.Acquire(true)
	releaseHigh()

	lp.TryCleanup(time.Now().Add(2 * time.Minute))
	if lp.MaybeHigh() != nil {
		t.Fatal("expected highPool to be nil after idle timeout cleanup")
	}
}

func TestReleaseSchedulesCleanupTimer(t *testing.T) {
	lp := pool.NewLazyDualPool(
		20*time.Millisecond,
		func() *pool.BytesPool { return pool.NewBytesPool(8) },
		func() *pool.BytesPool { return pool.NewBytesPool(16) },
	)

	high, release := lp.Acquire(true)
	if high == nil {
		t.Fatal("expected high pool to be initialized")
	}
	release()

	time.Sleep(40 * time.Millisecond)
	if got := lp.MaybeHigh(); got != nil {
		t.Fatal("expected high pool to be nil after scheduled cleanup")
	}
}
