package benchreport

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

const mib = 1024 * 1024

// Monitor captures CPU and heap metrics around a benchmark hot loop and prints a
// human-readable report via testing.B.Logf (visible with go test -v).
type Monitor struct {
	b        *testing.B
	name     string
	baseline runtime.MemStats
	peakAlloc atomic.Uint64
	peakSys   atomic.Uint64
	started   time.Time
	bytesPerOp int64
}

// Start prepares baseline memory sampling and registers a cleanup report hook.
// Call before b.ResetTimer(); invoke SamplePeriodically inside the hot loop.
func Start(b *testing.B, bytesPerOp int64) *Monitor {
	m := &Monitor{
		b:          b,
		name:       b.Name(),
		bytesPerOp: bytesPerOp,
	}
	m.captureBaseline()
	b.Cleanup(m.logReport)
	return m
}

func (m *Monitor) captureBaseline() {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	runtime.ReadMemStats(&m.baseline)
}

// MarkTimerStart records the wall-clock start of the timed section.
func (m *Monitor) MarkTimerStart() {
	m.started = time.Now()
}

// SamplePeriodically updates peak heap stats without adding measurable overhead.
func (m *Monitor) SamplePeriodically(iteration int) {
	if m.b.N <= 0 {
		return
	}
	every := m.b.N / 32
	if every < 1 {
		every = 1
	}
	if iteration%every != 0 && iteration != m.b.N-1 {
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.updatePeak(ms)
}

// SampleNow forces a peak memory sample (e.g. after setup, before hot loop).
func (m *Monitor) SampleNow() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.updatePeak(ms)
}

func (m *Monitor) updatePeak(ms runtime.MemStats) {
	for {
		cur := m.peakAlloc.Load()
		if ms.Alloc <= cur || m.peakAlloc.CompareAndSwap(cur, ms.Alloc) {
			break
		}
	}
	for {
		cur := m.peakSys.Load()
		if ms.Sys <= cur || m.peakSys.CompareAndSwap(cur, ms.Sys) {
			break
		}
	}
}

func (m *Monitor) logReport() {
	// Skip Go's short calibration passes (N=1 and other sub-threshold runs).
	if m.b.Elapsed() < 100*time.Millisecond {
		return
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	baselineMB := float64(m.baseline.Alloc) / mib
	peakMB := float64(m.peakAlloc.Load()) / mib
	if peakMB < baselineMB {
		peakMB = float64(after.Alloc) / mib
	}
	afterMB := float64(after.Alloc) / mib
	peakSysMB := float64(m.peakSys.Load()) / mib
	if peakSysMB == 0 {
		peakSysMB = float64(after.Sys) / mib
	}

	elapsed := m.b.Elapsed()
	if !m.started.IsZero() {
		elapsed = time.Since(m.started)
	}
	if elapsed <= 0 {
		elapsed = m.b.Elapsed()
	}

	ops := uint64(m.b.N)
	if ops == 0 {
		ops = 1
	}
	perOp := elapsed / time.Duration(ops)
	var throughputMBs float64
	if m.bytesPerOp > 0 && elapsed > 0 {
		throughputMBs = float64(m.bytesPerOp) * float64(ops) / elapsed.Seconds() / mib
	}

	totalAllocMB := float64(after.TotalAlloc-m.baseline.TotalAlloc) / mib
	gcDelta := int(after.NumGC - m.baseline.NumGC)

	m.b.Log("📊 Performance Report:")
	m.b.Logf("  Benchmark:       %s", m.name)
	m.b.Log("  CPU:")
	m.b.Logf("    Iterations:    %d", m.b.N)
	m.b.Logf("    Wall time:     %s", elapsed.Round(time.Microsecond))
	m.b.Logf("    Per op:        %s", formatDuration(perOp))
	if throughputMBs > 0 {
		m.b.Logf("    Throughput:    %.2f MB/s", throughputMBs)
	}
	m.b.Log("  RAM:")
	m.b.Logf("    Baseline:      %.2f MB", baselineMB)
	m.b.Logf("    Peak during:   %.2f MB (growth: %+.2f MB)", peakMB, peakMB-baselineMB)
	m.b.Logf("    After run:     %.2f MB (retained: %+.2f MB)", afterMB, afterMB-baselineMB)
	m.b.Logf("    Peak heap sys: %.2f MB", peakSysMB)
	m.b.Logf("    Total alloc:   %.2f MB (cumulative)", totalAllocMB)
	m.b.Logf("    GC runs:       %+d", gcDelta)
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Millisecond:
		return fmt.Sprintf("%.2f ms/op", float64(d.Microseconds())/1000)
	case d >= time.Microsecond:
		return fmt.Sprintf("%.2f µs/op", float64(d.Microseconds()))
	default:
		return fmt.Sprintf("%.1f ns/op", float64(d.Nanoseconds()))
	}
}
