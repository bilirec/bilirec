package swr

import (
	"runtime"
	"testing"
	"time"
)

func TestCacheStopReturnsWhenStoppedImmediatelyAfterStart(t *testing.T) {
	prev := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prev)

	cache := NewCache[string, int](50*time.Millisecond, 100*time.Millisecond, 4)
	cache.Start()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cache.Stop()
	}()

	select {
	case <-done:
		return
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Stop blocked when called immediately after Start; this reproduces the CI cleanup deadlock window")
	}
}
