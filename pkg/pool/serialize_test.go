package pool_test

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/pool"
)

type TaskQueue struct {
	TaskID string
	Status string
}

func TestSerializer_NoDuplicateTypeConcurrent(t *testing.T) {
	s := pool.NewSerializer()
	var wg sync.WaitGroup
	errCh := make(chan error, 200) // buffered so goroutines never block

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := TaskQueue{TaskID: fmt.Sprint(i)}
			data, err := s.Serialize(q)
			if err != nil {
				errCh <- fmt.Errorf("serialize %d: %w", i, err)
				return
			}
			var q2 TaskQueue
			if err := s.Deserialize(data, &q2); err != nil {
				errCh <- fmt.Errorf("deserialize %d: %w", i, err)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err) // run in main test goroutine
	}
}

func TestSerializer_BufferPoolLeak(t *testing.T) {
	// Test that Serializer's buffer pools are properly returned and do not leak.
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	s := pool.NewSerializer()

	const cycles = 8
	const perCycle = 500
	const payloadSize = 64 * 1024 // 64KB, to exercise buffer growth

	t.Logf("🔄 Running %d cycles × %d serializations (payload=%d KB)", cycles, perCycle, payloadSize/1024)

	bigStr := strings.Repeat("x", payloadSize)

	for cycle := 0; cycle < cycles; cycle++ {
		for i := 0; i < perCycle; i++ {
			q := TaskQueue{
				TaskID: fmt.Sprintf("cycle-%d-%d", cycle, i),
				Status: bigStr,
			}
			data, err := s.Serialize(q)
			if err != nil {
				t.Fatalf("serialize cycle %d i %d: %v", cycle, i, err)
			}
			var q2 TaskQueue
			if err := s.Deserialize(data, &q2); err != nil {
				t.Fatalf("deserialize cycle %d i %d: %v", cycle, i, err)
			}
		}

		// occasional GC to let the runtime return memory as it normally would
		if cycle%2 == 0 {
			runtime.GC()
		}
	}

	runtime.GC()
	time.Sleep(300 * time.Millisecond)
	runtime.ReadMemStats(&m2)

	baseline := float64(m1.Alloc) / (1024 * 1024)
	after := float64(m2.Alloc) / (1024 * 1024)
	growth := after - baseline

	t.Logf("📊 Serializer Buffer Pool Test:")
	t.Logf("  Baseline:  %.2f MB", baseline)
	t.Logf("  After:     %.2f MB", after)
	t.Logf("  Growth:    %.2f MB", growth)

	// Threshold: allow small fluctuations, but fail on large growth
	if growth > 10.0 {
		t.Errorf("⚠️  Buffer pool may be leaking: %.2f MB growth after %d cycles", growth, cycles)
	} else {
		t.Logf("✅ Buffer pool appears healthy")
	}
}
