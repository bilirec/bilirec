package processors_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

func TestBufferedStreamWriter_MemoryLeak(t *testing.T) {
	// Create temp directory for test files
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test_output.flv")

	// Create pipeline with buffered writer
	writerInfo := processors.NewBufferedStreamWriter(
		testFile,
		processors.WithBufferSize(5*1024*1024), // 5MB buffer
		processors.WithChanBufferSize(32),
		processors.WithBytesPool(pool.NewBytesPool(256*1024)), // align pool size with test chunk size
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("Failed to open pipeline: %v", err)
	}
	defer pipe.Close()

	// Memory tracking variables
	var m1, m2, m3, m4 runtime.MemStats

	// Force GC and get baseline memory
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.ReadMemStats(&m1)

	// Phase 1: Write a large amount of data (simulate streaming)
	const (
		chunkSize  = 256 * 1024 // 256 KB per chunk
		iterations = 1000       // 1000 iterations = ~250 MB
	)

	chunk := make([]byte, chunkSize)
	if _, err := rand.Read(chunk); err != nil {
		t.Fatalf("Failed to generate random data: %v", err)
	}

	t.Logf("🚀 Starting to write %d chunks of %d KB each...", iterations, chunkSize/1024)
	start := time.Now()

	for i := 0; i < iterations; i++ {
		if _, err := pipe.Process(ctx, chunk); err != nil {
			t.Fatalf("Failed to process chunk %d: %v", i, err)
		}

		// Log progress every 100 iterations
		if (i+1)%100 == 0 {
			runtime.ReadMemStats(&m2)
			t.Logf("Progress: %d/%d chunks, Memory: %.2f MB",
				i+1, iterations, float64(m2.Alloc)/(1024*1024))
		}
	}

	elapsed := time.Since(start)
	t.Logf("✅ Write phase complete: %d chunks in %v (%.2f MB/s)",
		iterations, elapsed, float64(iterations*chunkSize)/(1024*1024)/elapsed.Seconds())

	// Read memory after write phase (before GC)
	runtime.ReadMemStats(&m2)

	// Phase 2: Force GC and wait for cleanup
	t.Log("🧹 Running garbage collection...")
	runtime.GC()
	runtime.GC() // Double GC to ensure finalization
	time.Sleep(500 * time.Millisecond)
	runtime.ReadMemStats(&m3)

	// Phase 3: Close pipeline and measure final memory
	pipe.Close()
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
	runtime.ReadMemStats(&m4)

	// Memory analysis
	baselineAlloc := float64(m1.Alloc) / (1024 * 1024)
	afterWriteAlloc := float64(m2.Alloc) / (1024 * 1024)
	afterGCAlloc := float64(m3.Alloc) / (1024 * 1024)
	afterCloseAlloc := float64(m4.Alloc) / (1024 * 1024)

	peakGrowth := afterWriteAlloc - baselineAlloc
	retainedAfterGC := afterGCAlloc - baselineAlloc
	retainedAfterClose := afterCloseAlloc - baselineAlloc

	t.Logf("📊 Memory Statistics:")
	t.Logf("  Baseline:               %.2f MB", baselineAlloc)
	t.Logf("  After write:           %.2f MB (growth: +%.2f MB)", afterWriteAlloc, peakGrowth)
	t.Logf("  After GC:              %.2f MB (retained: +%.2f MB)", afterGCAlloc, retainedAfterGC)
	t.Logf("  After close:           %.2f MB (retained: +%.2f MB)", afterCloseAlloc, retainedAfterClose)
	t.Logf("  GC reclaimed:          %.2f MB", afterWriteAlloc-afterGCAlloc)
	t.Logf("  Close reclaimed:       %.2f MB", afterGCAlloc-afterCloseAlloc)

	// Verify output file
	stat, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Failed to stat output file: %v", err)
	}

	expectedSize := int64(iterations * chunkSize)
	actualSize := stat.Size()
	t.Logf("📁 File size: %.2f MB (expected: %.2f MB)",
		float64(actualSize)/(1024*1024), float64(expectedSize)/(1024*1024))

	if actualSize != expectedSize {
		t.Errorf("❌ File size mismatch: got %d bytes, expected %d bytes", actualSize, expectedSize)
	}

	// Memory leak detection thresholds.
	// We allow retained memory for runtime fragmentation + expected pool/buffer residency.
	// With chan=32 and chunk=256KB, expected pool residency is roughly <= 8MB.
	const (
		expectedPoolResidencyMB = 8.0
		runtimeSlackAfterGCMB   = 20.0
		runtimeSlackAfterClose  = 12.0
	)

	maxRetainedAfterGCMB := expectedPoolResidencyMB + runtimeSlackAfterGCMB
	maxRetainedAfterCloseMB := expectedPoolResidencyMB + runtimeSlackAfterClose

	// Check for memory leaks
	if retainedAfterGC > maxRetainedAfterGCMB {
		t.Logf("⚠️ Warning: high memory retained after GC (pre-close): %.2f MB (threshold: %.2f MB)",
			retainedAfterGC, maxRetainedAfterGCMB)
	} else {
		t.Logf("✅ Memory after GC is within acceptable range")
	}

	if retainedAfterClose > maxRetainedAfterCloseMB {
		t.Errorf("⚠️ Possible memory leak: %.2f MB retained after close (threshold: %.2f MB)",
			retainedAfterClose, maxRetainedAfterCloseMB)
	} else {
		t.Logf("✅ Memory after close is within acceptable range")
	}

	// Informational metric: sync.Pool intentionally keeps some buffers,
	// so GC efficiency is not used as a hard failure condition.
	gcEfficiency := (afterWriteAlloc - afterGCAlloc) / peakGrowth * 100
	t.Logf("📈 GC efficiency: %.1f%% of peak growth reclaimed", gcEfficiency)
}

func TestBufferedStreamWriter_ConcurrentMemoryLeak(t *testing.T) {
	// Test for memory leaks under concurrent scenarios
	tempDir := t.TempDir()

	var m1, m2, m3 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	const (
		numGoroutines    = 5
		chunksPerRoutine = 200
		chunkSize        = 128 * 1024 // 128 KB
	)

	t.Logf("🔄 Testing %d concurrent writers...", numGoroutines)

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			testFile := filepath.Join(tempDir, "test_concurrent_"+string(rune('0'+id))+".flv")
			writerInfo := processors.NewBufferedStreamWriter(
				testFile,
				processors.WithBufferSize(5*1024*1024),
				processors.WithChanBufferSize(16),
				processors.WithBytesPool(pool.NewBytesPool(chunkSize)),
			)
			pipe := pipeline.New(writerInfo)

			ctx := context.Background()
			if err := pipe.Open(ctx); err != nil {
				t.Errorf("Goroutine %d:  Failed to open pipeline: %v", id, err)
				return
			}
			defer pipe.Close()

			chunk := make([]byte, chunkSize)
			rand.Read(chunk)

			for j := 0; j < chunksPerRoutine; j++ {
				if _, err := pipe.Process(ctx, chunk); err != nil {
					t.Errorf("Goroutine %d: Failed to process chunk %d:  %v", id, j, err)
					return
				}
			}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	runtime.ReadMemStats(&m2)
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
	runtime.ReadMemStats(&m3)

	baseline := float64(m1.Alloc) / (1024 * 1024)
	afterWrite := float64(m2.Alloc) / (1024 * 1024)
	afterGC := float64(m3.Alloc) / (1024 * 1024)

	t.Logf("📊 Concurrent Memory Stats:")
	t.Logf("  Baseline:    %.2f MB", baseline)
	t.Logf("  After write: %.2f MB (growth: +%.2f MB)", afterWrite, afterWrite-baseline)
	t.Logf("  After GC:     %.2f MB (retained: +%.2f MB)", afterGC, afterGC-baseline)

	if (afterGC - baseline) > 25.0 {
		t.Errorf("⚠️ Possible memory leak in concurrent scenario: %.2f MB retained", afterGC-baseline)
	} else {
		t.Logf("✅ Concurrent memory usage is acceptable")
	}
}

func TestBufferedStreamWriter_LongRunningMemoryProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping long-running test in short mode")
	}

	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test_long_running.flv")

	writerInfo := processors.NewBufferedStreamWriter(
		testFile,
		processors.WithBufferSize(5*1024*1024),
		processors.WithChanBufferSize(16),
		processors.WithBytesPool(pool.NewBytesPool(128*1024)),
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("Failed to open pipeline: %v", err)
	}
	defer pipe.Close()

	// Simulate a long-running recording session
	const (
		chunkSize = 256 * 1024 // 256 KB
		duration  = 30 * time.Second
	)

	chunk := make([]byte, chunkSize)
	rand.Read(chunk)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	done := time.After(duration)
	memSamples := []float64{}
	iterations := 0

	t.Logf("⏱️ Running long-duration test for %v...", duration)

	var m runtime.MemStats

sampleLoop:
	for {
		select {
		case <-ticker.C:
			// Process chunk
			if _, err := pipe.Process(ctx, chunk); err != nil {
				t.Fatalf("Failed to process chunk: %v", err)
			}
			iterations++

			// Sample memory every 10 iterations
			if iterations%10 == 0 {
				runtime.ReadMemStats(&m)
				currentMem := float64(m.Alloc) / (1024 * 1024)
				memSamples = append(memSamples, currentMem)
			}

		case <-done:
			break sampleLoop
		}
	}

	t.Logf("✅ Processed %d chunks over %v", iterations, duration)

	// Analyze memory trend
	if len(memSamples) > 2 {
		firstHalf := average(memSamples[:len(memSamples)/2])
		secondHalf := average(memSamples[len(memSamples)/2:])
		trend := secondHalf - firstHalf

		t.Logf("📈 Memory trend analysis:")
		t.Logf("  First half avg:   %.2f MB", firstHalf)
		t.Logf("  Second half avg: %.2f MB", secondHalf)
		t.Logf("  Trend:           %+.2f MB", trend)

		// If memory grows more than 20 MB over time, it's likely a leak
		if trend > 20.0 {
			t.Errorf("⚠️ Memory appears to be growing over time: +%.2f MB", trend)
		} else {
			t.Logf("✅ Memory usage is stable over time")
		}
	}
}

func TestBufferedStreamWriter_NoReturnedDataLeak(t *testing.T) {
	// Specifically test that returning 'data' vs 'nil' doesn't cause leaks
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test_return_leak.flv")

	writerInfo := processors.NewBufferedStreamWriter(
		testFile,
		processors.WithBufferSize(5*1024*1024),
		processors.WithChanBufferSize(16),
		processors.WithBytesPool(pool.NewBytesPool(128*1024)),
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("Failed to open pipeline: %v", err)
	}
	defer pipe.Close()

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	const iterations = 500
	dataChunks := make([][]byte, iterations)

	// Pre-allocate all chunks
	for i := range dataChunks {
		dataChunks[i] = make([]byte, 128*1024)
		rand.Read(dataChunks[i])
	}

	// Process all chunks
	for i, chunk := range dataChunks {
		returned, err := pipe.Process(ctx, chunk)
		if err != nil {
			t.Fatalf("Failed to process chunk %d:  %v", i, err)
		}

		// Verify returned data is the same reference (pass-through)
		if len(returned) != len(chunk) {
			t.Errorf("Returned data length mismatch at chunk %d", i)
		}
		// Prevent accidentally retaining the returned reference in this loop.
		returned = nil
	}

	// Release test-owned references before GC measurement.
	dataChunks = nil

	runtime.ReadMemStats(&m2)
	runtime.GC()
	time.Sleep(300 * time.Millisecond)
	var m3 runtime.MemStats
	runtime.ReadMemStats(&m3)

	baseAlloc := float64(m1.Alloc) / (1024 * 1024)
	afterAlloc := float64(m2.Alloc) / (1024 * 1024)
	finalAlloc := float64(m3.Alloc) / (1024 * 1024)

	t.Logf("📊 Return value leak check:")
	t.Logf("  Baseline:  %.2f MB", baseAlloc)
	t.Logf("  After:     %.2f MB", afterAlloc)
	t.Logf("  Final:    %.2f MB", finalAlloc)

	// Most memory should be from pre-allocated chunks, which is expected
	// After GC, we should see cleanup
	if (finalAlloc - baseAlloc) > 20.0 {
		t.Errorf("⚠️ Possible leak from returned data: %.2f MB retained", finalAlloc-baseAlloc)
	} else {
		t.Logf("✅ No leak detected from returned data references")
	}
}

// Helper function to calculate average
func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func TestBufferedStreamWriter_SDCardProtection_SkipsFlushWhenUnderBufferSize(t *testing.T) {
	const bufferSize = 1 * 1024 * 1024 // 1MB

	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "small_write.flv")

	writerInfo := processors.NewBufferedStreamWriter(testFile,
		processors.WithBufferSize(bufferSize),
		processors.WithSDCardProtection(true),
		processors.WithSkipSmallFlushThreshold(bufferSize),
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("failed to open pipeline: %v", err)
	}

	// write less than bufferSize total
	smallChunk := make([]byte, 512*1024) // 512KB < 1MB buffer
	if _, err := rand.Read(smallChunk); err != nil {
		t.Fatalf("failed to generate random data: %v", err)
	}
	if _, err := pipe.Process(ctx, smallChunk); err != nil {
		t.Fatalf("failed to process chunk: %v", err)
	}

	pipe.Close()

	_, err := os.Stat(testFile)
	if err == nil {
		t.Fatalf("expected no output file under SD card protection when total written < bufferSize")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("unexpected stat error for output file: %v", err)
	}
}

func TestBufferedStreamWriter_SDCardProtection_WritesNormallyWhenOverBufferSize(t *testing.T) {
	const bufferSize = 1 * 1024 * 1024 // 1MB

	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "large_write.flv")

	writerInfo := processors.NewBufferedStreamWriter(testFile,
		processors.WithBufferSize(bufferSize),
		processors.WithSDCardProtection(true),
		processors.WithSkipSmallFlushThreshold(bufferSize),
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("failed to open pipeline: %v", err)
	}

	// write more than bufferSize total to trigger bufio auto-flush
	chunk := make([]byte, 256*1024) // 256KB per chunk
	if _, err := rand.Read(chunk); err != nil {
		t.Fatalf("failed to generate random data: %v", err)
	}
	totalWritten := 0
	for totalWritten < bufferSize+256*1024 { // write ~1.25MB
		if _, err := pipe.Process(ctx, chunk); err != nil {
			t.Fatalf("failed to process chunk: %v", err)
		}
		totalWritten += len(chunk)
	}

	pipe.Close()

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat output file: %v", err)
	}

	if info.Size() == 0 {
		t.Errorf("expected file to have data when total written (%d) >= bufferSize (%d)", totalWritten, bufferSize)
	}
}

func TestBufferedStreamWriter_SDCardProtection_WritesAfterThresholdWithPeriodicFlush(t *testing.T) {
	const (
		bufferSize = 512 * 1024 // 512KB
		threshold  = 512 * 1024 // 512KB
	)

	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "threshold_write.flv")

	writerInfo := processors.NewBufferedStreamWriter(testFile,
		processors.WithBufferSize(bufferSize),
		processors.WithSDCardProtection(true),
		processors.WithSkipSmallFlushThreshold(threshold),
		processors.WithChanBufferSize(4),
		processors.WithFlushPeriod(10*time.Millisecond),
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("failed to open pipeline: %v", err)
	}

	chunk := make([]byte, 256*1024)
	if _, err := rand.Read(chunk); err != nil {
		t.Fatalf("failed to generate random data: %v", err)
	}

	if _, err := pipe.Process(ctx, chunk); err != nil {
		t.Fatalf("failed to process first chunk: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		if err != nil {
			t.Fatalf("unexpected stat error after first chunk: %v", err)
		}
		t.Fatalf("expected file not to exist after first chunk")
	}

	if _, err := pipe.Process(ctx, chunk); err != nil {
		t.Fatalf("failed to process second chunk: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("expected file to exist after threshold, got: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat output file after flush: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected file to have data after periodic flush, got 0")
	}

	if _, err := pipe.Process(ctx, chunk); err != nil {
		t.Fatalf("failed to process third chunk: %v", err)
	}
	if _, err := pipe.Process(ctx, chunk); err != nil {
		t.Fatalf("failed to process fourth chunk: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	info2, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("expected file to still exist after third chunk, got: %v", err)
	}
	if info2.Size() <= info.Size() {
		t.Fatalf("expected file size to grow after third chunk; before=%d after=%d", info.Size(), info2.Size())
	}

	pipe.Close()
}

func TestBufferedStreamWriter_NoSDCardProtection_AlwaysFlushes(t *testing.T) {
	const bufferSize = 1 * 1024 * 1024 // 1MB

	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "no_protection.flv")

	writerInfo := processors.NewBufferedStreamWriter(testFile,
		processors.WithBufferSize(bufferSize),
		// sdcardProtection defaults to false
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("failed to open pipeline: %v", err)
	}

	// write less than bufferSize — without protection, Close should still flush
	smallChunk := make([]byte, 512*1024) // 512KB
	if _, err := rand.Read(smallChunk); err != nil {
		t.Fatalf("failed to generate random data: %v", err)
	}
	if _, err := pipe.Process(ctx, smallChunk); err != nil {
		t.Fatalf("failed to process chunk: %v", err)
	}

	pipe.Close()

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat output file: %v", err)
	}

	if info.Size() == 0 {
		t.Errorf("expected file to have data when SD card protection is disabled, got 0 bytes")
	}
}

func init() {
	_ = bufio.NewWriter(nil)
	// Set log level to warn to reduce noise in tests
	logrus.SetLevel(logrus.WarnLevel)
}
