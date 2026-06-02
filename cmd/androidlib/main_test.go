//go:build cgo && android

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/fx"
)

func TestSyntheticFrequentStopPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("skip synthetic stop pressure in short mode")
	}

	_ = Stop()
	t.Cleanup(func() {
		_ = Stop()
	})

	const rounds = 300
	for i := 1; i <= rounds; i++ {
		app := newSyntheticApp(2 * time.Millisecond)
		startCtx, cancelStart := context.WithTimeout(context.Background(), 2*time.Second)
		if err := app.Start(startCtx); err != nil {
			cancelStart()
			t.Fatalf("round %d: synthetic app start failed: %v", i, err)
		}
		cancelStart()

		appMu.Lock()
		androidApp = app
		appMu.Unlock()

		if code := Stop(); code != 0 {
			t.Fatalf("round %d: Stop() returned %d", i, code)
		}
	}
}

func TestSyntheticConcurrentStopPressure(t *testing.T) {
	_ = Stop()
	t.Cleanup(func() {
		_ = Stop()
	})

	app := newSyntheticApp(5 * time.Millisecond)
	startCtx, cancelStart := context.WithTimeout(context.Background(), 2*time.Second)
	if err := app.Start(startCtx); err != nil {
		cancelStart()
		t.Fatalf("synthetic app start failed: %v", err)
	}
	cancelStart()

	appMu.Lock()
	androidApp = app
	appMu.Unlock()

	const goroutines = 24
	var failures atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if code := Stop(); code != 0 {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()

	if failures.Load() != 0 {
		t.Fatalf("concurrent Stop pressure got %d non-zero returns", failures.Load())
	}
}

func newSyntheticApp(stopDelay time.Duration) *fx.App {
	return fx.New(
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{
				OnStop: func(ctx context.Context) error {
					select {
					case <-time.After(stopDelay):
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				},
			})
		}),
		fx.StartTimeout(2*time.Second),
		fx.StopTimeout(2*time.Second),
	)
}
