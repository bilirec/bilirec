package flv_test

import (
	"crypto/rand"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/utils"
)

func TestRealtimeFixer_MemoryLeak(t *testing.T) {
	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()

	var m1, m2, m3, m4 runtime.MemStats

	// Baseline memory
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.ReadMemStats(&m1)

	const (
		chunkSize  = 128 * 1024 // 128 KB per chunk
		iterations = 2000       // ~250 MB total
	)

	// Generate realistic FLV header + data
	flvHeader := []byte{
		'F', 'L', 'V', // Signature
		0x01,                   // Version
		0x05,                   // TypeFlags (audio + video)
		0x00, 0x00, 0x00, 0x09, // DataOffset
	}

	// Generate random FLV-like data chunks
	chunk := generateFLVChunk(chunkSize)

	t.Logf("🔧 Testing RealtimeFixer with %d iterations of %d KB chunks", iterations, chunkSize/1024)

	// Phase 1: Feed header
	_, err := fixer.Fix(flvHeader)
	if err != nil {
		t.Fatalf("Failed to process FLV header: %v", err)
	}

	writeHeapProfile(t, "heap_before.prof")

	// Phase 2: Process chunks
	start := time.Now()
	var totalOutput int64

	for i := 0; i < iterations; i++ {
		output, err := fixer.Fix(chunk)
		if err != nil {
			t.Fatalf("Failed to process chunk %d: %v", i, err)
		}
		totalOutput += int64(len(output))

		// Sample memory periodically
		if (i+1)%200 == 0 {
			runtime.ReadMemStats(&m2)
			t.Logf("Progress: %d/%d chunks, Memory: %.2f MB, Output: %.2f MB",
				i+1, iterations,
				float64(m2.Alloc)/(1024*1024),
				float64(totalOutput)/(1024*1024))
		}
	}

	elapsed := time.Since(start)
	t.Logf("✅ Processing complete in %v (%.2f MB/s)",
		elapsed, float64(iterations*chunkSize)/(1024*1024)/elapsed.Seconds())

	// Memory after processing
	runtime.ReadMemStats(&m2)

	writeHeapProfile(t, "heap_after_process.prof")

	// Phase 3: Force GC
	t.Log("🧹 Running garbage collection...")
	runtime.GC()
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
	runtime.ReadMemStats(&m3)

	writeHeapProfile(t, "heap_after_gc.prof")

	// Phase 4: Close fixer and measure final memory
	fixer.Close()
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
	runtime.ReadMemStats(&m4)

	writeHeapProfile(t, "heap_after_close.prof")

	// Analyze memory
	baseline := float64(m1.Alloc) / (1024 * 1024)
	afterProcess := float64(m2.Alloc) / (1024 * 1024)
	afterGC := float64(m3.Alloc) / (1024 * 1024)
	afterClose := float64(m4.Alloc) / (1024 * 1024)

	t.Logf("📊 Memory Analysis:")
	t.Logf("  Baseline:        %.2f MB", baseline)
	t.Logf("  After process:   %.2f MB (growth: +%.2f MB)", afterProcess, afterProcess-baseline)
	t.Logf("  After GC:        %.2f MB (retained: +%.2f MB)", afterGC, afterGC-baseline)
	t.Logf("  After close:    %.2f MB (retained: +%.2f MB)", afterClose, afterClose-baseline)
	t.Logf("  GC reclaimed:   %.2f MB", afterProcess-afterGC)
	t.Logf("  Close reclaimed: %.2f MB", afterGC-afterClose)

	// Get dedup stats
	dups, cacheSize, cacheCapacity := fixer.GetDedupStats()
	t.Logf("🗂️  Dedup Stats:  %d duplicates, cache:  %d/%d", dups, cacheSize, cacheCapacity)

	// Memory leak detection
	const (
		maxRetainedAfterGC    = 15.0 // MB
		maxRetainedAfterClose = 8.0  // MB
	)

	if afterGC-baseline > maxRetainedAfterGC {
		t.Logf("⚠️  Warning: high memory retained after GC (pre-close): %.2f MB (threshold: %.2f MB)",
			afterGC-baseline, maxRetainedAfterGC)
		t.Logf("📁 Heap profiles saved:")
		t.Logf("   - heap_before.prof")
		t.Logf("   - heap_after_process. prof")
		t.Logf("   - heap_after_gc.prof")
		t.Logf("   - heap_after_close.prof")
		t.Logf("🔍 Analyze with: go tool pprof -http=:8080 heap_after_gc.prof")
	} else {
		t.Logf("✅ Memory after GC within acceptable range")
	}

	if afterClose-baseline > maxRetainedAfterClose {
		t.Errorf("⚠️  Possible memory leak: %.2f MB retained after close (threshold: %.2f MB)",
			afterClose-baseline, maxRetainedAfterClose)
	} else {
		t.Logf("✅ Memory after close within acceptable range")
	}

	// Check GC efficiency
	gcEfficiency := (afterProcess - afterGC) / (afterProcess - baseline) * 100
	t.Logf("📈 GC efficiency: %.1f%%", gcEfficiency)

	if gcEfficiency < 75.0 {
		log := utils.Ternary(os.Getenv("CI") != "", t.Logf, t.Errorf)
		log("⚠️  Low GC efficiency: %.1f%% (expected > 75%%)", gcEfficiency)
	}
}

func TestRealtimeFixer_HeapAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping heap analysis in short mode")
	}

	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()

	// FLV header
	flvHeader := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
	fixer.Fix(flvHeader)

	const iterations = 500
	chunk := generateFLVChunk(128 * 1024)

	t.Log("📊 Running heap analysis...")

	// Snapshot at intervals
	snapshots := []int{0, 100, 200, 300, 400, 500}

	for i := 0; i < iterations; i++ {
		output, err := fixer.Fix(chunk)
		if err != nil {
			t.Fatalf("Failed at iteration %d: %v", i, err)
		}
		_ = output   // Use output
		output = nil // Clear reference

		// Take snapshots
		for _, snap := range snapshots {
			if i == snap {
				runtime.GC()
				time.Sleep(100 * time.Millisecond)

				var m runtime.MemStats
				runtime.ReadMemStats(&m)

				filename := fmt.Sprintf("heap_iter_%04d.prof", i)
				writeHeapProfile(t, filename)

				t.Logf("Snapshot %d: %.2f MB allocated", i, float64(m.Alloc)/(1024*1024))
			}
		}
	}

	t.Log("📁 Heap snapshots saved.  Compare with:")
	t.Log("   go tool pprof -base heap_iter_0000.prof heap_iter_0500.prof")
	t.Log("   Then run 'top' or 'list' to see what grew")
}

func TestRealtimeFixer_AllocationTracking(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping allocation tracking in short mode")
	}

	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()

	// FLV header
	flvHeader := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
	fixer.Fix(flvHeader)

	chunk := generateFLVChunk(128 * 1024)

	// Enable CPU profiling to see allocation sites
	cpuFile, err := os.Create("cpu_alloc.prof")
	if err != nil {
		t.Fatalf("Could not create CPU profile: %v", err)
	}
	defer cpuFile.Close()

	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		t.Fatalf("Could not start CPU profile: %v", err)
	}
	defer pprof.StopCPUProfile()

	// Run iterations
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		output, _ := fixer.Fix(chunk)
		_ = output
		output = nil
	}

	t.Log("📁 CPU profile saved to cpu_alloc.prof")
	t.Log("🔍 Analyze with: go tool pprof -http=:8080 cpu_alloc.prof")
}

func TestRealtimeFixer_BufferPoolLeak(t *testing.T) {
	// Test that buffer pools are properly returned

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	const cycles = 10
	const chunksPerCycle = 100

	t.Logf("🔄 Testing buffer pool with %d cycles of %d chunks each", cycles, chunksPerCycle)

	for cycle := 0; cycle < cycles; cycle++ {
		fixer := flv.NewRealtimeFixer()

		// FLV header
		flvHeader := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
		fixer.Fix(flvHeader)

		// Process chunks
		chunk := generateFLVChunk(64 * 1024)
		for i := 0; i < chunksPerCycle; i++ {
			_, err := fixer.Fix(chunk)
			if err != nil {
				t.Fatalf("Cycle %d, chunk %d: %v", cycle, i, err)
			}
		}

		// Close and cleanup
		fixer.Close()

		// Force GC after each cycle
		if cycle%3 == 0 {
			runtime.GC()
		}
	}

	runtime.GC()
	time.Sleep(300 * time.Millisecond)
	runtime.ReadMemStats(&m2)

	baseline := float64(m1.Alloc) / (1024 * 1024)
	afterCycles := float64(m2.Alloc) / (1024 * 1024)
	growth := afterCycles - baseline

	t.Logf("📊 Buffer Pool Test:")
	t.Logf("  Baseline:  %.2f MB", baseline)
	t.Logf("  After %d cycles: %.2f MB", cycles, afterCycles)
	t.Logf("  Growth: %.2f MB", growth)

	// Should have minimal growth if pools are working correctly
	if growth > 10.0 {
		t.Errorf("⚠️  Buffer pool may be leaking: %.2f MB growth after %d cycles", growth, cycles)
	} else {
		t.Logf("✅ Buffer pool appears healthy")
	}
}

func TestRealtimeFixer_LongRunningStability(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping long-running test in short mode")
	}

	fixer := flv.NewRealtimeFixer()
	defer fixer.Close()

	// FLV header
	flvHeader := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
	fixer.Fix(flvHeader)

	const (
		duration  = 60 * time.Second
		chunkSize = 128 * 1024
	)

	chunk := generateFLVChunk(chunkSize)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	done := time.After(duration)
	memSamples := []float64{}
	iterations := 0

	t.Logf("⏱️  Running long-duration stability test for %v", duration)

	var m runtime.MemStats

loop:
	for {
		select {
		case <-ticker.C:
			_, err := fixer.Fix(chunk)
			if err != nil {
				t.Fatalf("Failed at iteration %d: %v", iterations, err)
			}
			iterations++

			// Sample memory every 20 iterations
			if iterations%20 == 0 {
				runtime.ReadMemStats(&m)
				currentMem := float64(m.Alloc) / (1024 * 1024)
				memSamples = append(memSamples, currentMem)
			}

		case <-done:
			break loop
		}
	}

	t.Logf("✅ Processed %d chunks over %v", iterations, duration)

	// Analyze memory trend
	if len(memSamples) > 4 {
		firstQuarter := average(memSamples[:len(memSamples)/4])
		lastQuarter := average(memSamples[len(memSamples)*3/4:])
		trend := lastQuarter - firstQuarter

		t.Logf("📈 Memory trend analysis:")
		t.Logf("  First quarter avg: %.2f MB", firstQuarter)
		t.Logf("  Last quarter avg:   %.2f MB", lastQuarter)
		t.Logf("  Trend:            %+.2f MB", trend)

		// Memory should be relatively stable
		if trend > 25.0 {
			t.Errorf("⚠️  Memory growing over time: +%.2f MB", trend)
		} else if trend < -5.0 {
			t.Logf("✅ Memory decreasing (GC working well)")
		} else {
			t.Logf("✅ Memory stable over time")
		}
	}

	// Check dedup cache growth
	dups, cacheSize, cacheCapacity := fixer.GetDedupStats()
	t.Logf("🗂️  Final Dedup Stats: %d duplicates, cache: %d/%d (%.1f%% full)",
		dups, cacheSize, cacheCapacity, float64(cacheSize)/float64(cacheCapacity)*100)
}

func TestRealtimeFixer_TagPoolLeak(t *testing.T) {
	// Specifically test that Tag objects are properly returned to pool

	var m1, m2, m3 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	const iterations = 500

	t.Log("🏷️  Testing Tag pool recycling...")

	fixer := flv.NewRealtimeFixer()

	// FLV header
	flvHeader := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
	fixer.Fix(flvHeader)

	// Process many small chunks (more tags = more pool pressure)
	smallChunk := generateFLVChunk(16 * 1024)

	for i := 0; i < iterations; i++ {
		_, err := fixer.Fix(smallChunk)
		if err != nil {
			t.Fatalf("Failed at iteration %d: %v", i, err)
		}
	}

	runtime.ReadMemStats(&m2)
	fixer.Close()
	// sync.Pool and runtime allocator reclaim timing is non-deterministic.
	// Run GC twice and wait a bit longer to reduce test flakiness.
	runtime.GC()
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
	runtime.ReadMemStats(&m3)

	baseline := float64(m1.Alloc) / (1024 * 1024)
	afterProcess := float64(m2.Alloc) / (1024 * 1024)
	afterClose := float64(m3.Alloc) / (1024 * 1024)

	t.Logf("📊 Tag Pool Analysis:")
	t.Logf("  Baseline:       %.2f MB", baseline)
	t.Logf("  After process:  %.2f MB", afterProcess)
	t.Logf("  After close:   %.2f MB", afterClose)
	t.Logf("  Retained:      %.2f MB", afterClose-baseline)

	// Keep threshold aligned with other RealtimeFixer memory tests.
	// sync.Pool can retain memory between runs; this is not necessarily a leak.
	const maxRetainedAfterClose = 8.0 // MB
	if afterClose-baseline > maxRetainedAfterClose {
		t.Errorf("⚠️  Tag pool may be leaking: %.2f MB retained (threshold: %.2f MB)", afterClose-baseline, maxRetainedAfterClose)
	} else {
		t.Logf("✅ Tag pool working correctly")
	}
}

func BenchmarkRealtimeFixer_Fix_1MBChunk(b *testing.B) {
	fixer := flv.NewRealtimeFixer(
		flv.WithBufferSizes(1024*1024, 1024*1024),
	)
	defer fixer.Close()

	chunk := generateFLVChunk(1024 * 1024)
	if _, err := fixer.Fix(flv.FlvHeader); err != nil {
		b.Fatalf("flv header: %v", err)
	}

	var sink []byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := fixer.Fix(chunk)
		if err != nil {
			b.Fatalf("fix chunk: %v", err)
		}
		sink = out
	}
	_ = sink
}

// ✅ Helper to write heap profile
func writeHeapProfile(t *testing.T, filename string) {
	f, err := os.Create(filename)
	if err != nil {
		t.Logf("⚠️  Could not create heap profile %s: %v", filename, err)
		return
	}
	defer f.Close()

	runtime.GC() // Force GC before taking snapshot
	if err := pprof.WriteHeapProfile(f); err != nil {
		t.Logf("⚠️  Could not write heap profile %s: %v", filename, err)
	}
}

func TestRealtimeFixer_SharedPoolReleasesAfterClose(t *testing.T) {
	const chunkSize = 512 * 1024
	sharedPool := pool.NewBufferPool(
		chunkSize,
		chunkSize,
		pool.WithBoundedMode(true),
		pool.WithBoundedCapacity(2),
	)

	var m1, m2 runtime.MemStats
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.ReadMemStats(&m1)

	header := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
	chunk := generateFLVChunk(chunkSize)

	for session := 0; session < 3; session++ {
		fixer := flv.NewRealtimeFixer(
			flv.WithBufferPool(sharedPool),
			flv.WithBufferSizes(chunkSize, chunkSize),
		)
		if _, err := fixer.Fix(header); err != nil {
			t.Fatalf("session %d header: %v", session, err)
		}
		for i := 0; i < 200; i++ {
			if _, err := fixer.Fix(chunk); err != nil {
				t.Fatalf("session %d chunk %d: %v", session, i, err)
			}
		}
		fixer.Close()
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(300 * time.Millisecond)
	runtime.ReadMemStats(&m2)

	growthMB := float64(int64(m2.Alloc)-int64(m1.Alloc)) / (1024 * 1024)
	t.Logf("shared pool 3-session growth: %.2f MB", growthMB)
	if growthMB > 8.0 {
		t.Errorf("shared pool retained %.2f MB after 3 sessions (threshold 8 MB)", growthMB)
	}
}

// Helper function to generate FLV-like chunk data with only complete tags.
// Each tag is PrevTagSize + TagHeader + payload; partial tags at the end are omitted
// so repeated Fix() calls stay aligned.
func generateFLVChunk(size int) []byte {
	const tagPayloadSize = 100
	tagTotal := flv.PrevTagSizeBytes + flv.TagHeaderSize + tagPayloadSize

	chunk := make([]byte, size)
	offset := 0

	for offset+tagTotal <= size {
		offset += flv.PrevTagSizeBytes // PreviousTagSize (zero is fine for test data)

		chunk[offset] = flv.TagTypeAudio
		chunk[offset+1] = byte(tagPayloadSize >> 16)
		chunk[offset+2] = byte(tagPayloadSize >> 8)
		chunk[offset+3] = byte(tagPayloadSize)
		offset += flv.TagHeaderSize

		rand.Read(chunk[offset : offset+tagPayloadSize])
		offset += tagPayloadSize
	}

	return chunk[:offset]
}

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
