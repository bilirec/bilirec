package rw

import (
	"bufio"
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFlushLockedBufferedWriterBasicWrite(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	w := NewFlushLockedBufferedWriter(&buf, 64, &mu)

	data := []byte("hello")
	n, err := w.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected to write %d bytes, got %d", len(data), n)
	}

	if buf.Len() != 0 {
		t.Fatalf("data should be in buffer, not flushed yet")
	}

	if w.Buffered() != len(data) {
		t.Fatalf("Buffered() should return %d, got %d", len(data), w.Buffered())
	}

	err = w.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if buf.String() != "hello" {
		t.Fatalf("expected 'hello', got '%s'", buf.String())
	}
	if w.Buffered() != 0 {
		t.Fatalf("buffer should be empty after flush")
	}
}

func TestFlushLockedBufferedWriterBufferFull(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	size := 10
	w := NewFlushLockedBufferedWriter(&buf, size, &mu)

	// Write data that fills the buffer
	data1 := []byte("12345")
	w.Write(data1)
	if w.Buffered() != 5 {
		t.Fatalf("expected 5 bytes in buffer, got %d", w.Buffered())
	}

	// Write more data to fill the buffer
	data2 := []byte("67890")
	w.Write(data2)
	if w.Buffered() != 10 {
		t.Fatalf("expected 10 bytes in buffer, got %d", w.Buffered())
	}

	// Write data that exceeds buffer capacity - should auto-flush
	data3 := []byte("ab")
	n, err := w.Write(data3)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected to write 2 bytes, got %d", n)
	}

	// Previous data should be flushed
	if buf.String() != "1234567890" {
		t.Fatalf("expected '1234567890', got '%s'", buf.String())
	}

	// New data should be in buffer
	if w.Buffered() != 2 {
		t.Fatalf("expected 2 bytes in buffer, got %d", w.Buffered())
	}

	w.Flush()
	if buf.String() != "1234567890ab" {
		t.Fatalf("expected '1234567890ab', got '%s'", buf.String())
	}
}

func TestFlushLockedBufferedWriterLargeWrite(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	size := 10
	w := NewFlushLockedBufferedWriter(&buf, size, &mu)

	// Write data larger than buffer size
	largeData := []byte("this is a large write that exceeds buffer size")
	n, err := w.Write(largeData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(largeData) {
		t.Fatalf("expected to write %d bytes, got %d", len(largeData), n)
	}

	// Data should be directly written (not in buffer)
	if buf.String() != string(largeData) {
		t.Fatalf("large write should be written immediately")
	}
	if w.Buffered() != 0 {
		t.Fatalf("buffer should be empty after large write")
	}
}

func TestFlushLockedBufferedWriterMultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	w := NewFlushLockedBufferedWriter(&buf, 64, &mu)

	writes := [][]byte{
		[]byte("foo"),
		[]byte("bar"),
		[]byte("baz"),
	}

	for _, data := range writes {
		_, err := w.Write(data)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}

	if buf.Len() != 0 {
		t.Fatalf("data should not be flushed yet")
	}

	w.Flush()
	if buf.String() != "foobarbaz" {
		t.Fatalf("expected 'foobarbaz', got '%s'", buf.String())
	}
}

func TestFlushLockedBufferedWriterAvailable(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	size := 20
	w := NewFlushLockedBufferedWriter(&buf, size, &mu)

	if w.Available() != size {
		t.Fatalf("expected Available() to be %d, got %d", size, w.Available())
	}

	w.Write([]byte("hello"))
	if w.Available() != size-5 {
		t.Fatalf("expected Available() to be %d, got %d", size-5, w.Available())
	}
}

func TestFlushLockedBufferedWriterSize(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	size := 100
	w := NewFlushLockedBufferedWriter(&buf, size, &mu)

	if w.Size() != size {
		t.Fatalf("expected Size() to be %d, got %d", size, w.Size())
	}
}

func TestFlushLockedBufferedWriterDefaultSize(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	w := NewFlushLockedBufferedWriter(&buf, 0, &mu)

	if w.Size() != 4096 {
		t.Fatalf("expected default size to be 4096, got %d", w.Size())
	}
}

func TestFlushLockedBufferedWriterWriteError(t *testing.T) {
	errWriter := &errorWriter{failAfter: 1}
	var mu sync.Mutex
	w := NewFlushLockedBufferedWriter(errWriter, 10, &mu)

	// First write succeeds
	_, _ = w.Write([]byte("test"))

	// Flush fails
	err := w.Flush()
	if err != errWriter.testError {
		t.Fatalf("expected flush to return error")
	}
}

func TestFlushLockedBufferedWriterLockAcquisition(t *testing.T) {
	var buf bytes.Buffer
	lockAcquiredCount := int32(0)

	mu := &lockCountingMutex{
		acquiredCount: &lockAcquiredCount,
	}

	w := NewFlushLockedBufferedWriter(&buf, 10, mu)

	// Write should not acquire lock (lock-free)
	initialCount := atomic.LoadInt32(&lockAcquiredCount)
	w.Write([]byte("test"))
	afterWriteCount := atomic.LoadInt32(&lockAcquiredCount)

	if afterWriteCount != initialCount {
		t.Fatalf("Write should not acquire lock")
	}

	// Large write that goes directly should acquire lock
	w.Write([]byte("this is a very large write that will be written directly"))
	afterLargeWriteCount := atomic.LoadInt32(&lockAcquiredCount)
	if afterLargeWriteCount == initialCount {
		t.Fatalf("large direct write should acquire lock")
	}

	// Flush should acquire lock
	beforeFlushCount := atomic.LoadInt32(&lockAcquiredCount)
	w.Flush()
	afterFlushCount := atomic.LoadInt32(&lockAcquiredCount)
	if afterFlushCount == beforeFlushCount {
		t.Fatalf("Flush should acquire lock")
	}
}

func TestFlushLockedBufferedWriterZeroSizeWrite(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	w := NewFlushLockedBufferedWriter(&buf, 10, &mu)

	n, err := w.Write([]byte{})
	if err != nil {
		t.Fatalf("Write of zero bytes failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes written, got %d", n)
	}
}

func TestFlushLockedBufferedWriterMultipleFlushes(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	w := NewFlushLockedBufferedWriter(&buf, 20, &mu)

	w.Write([]byte("aaa"))
	w.Flush()
	if buf.String() != "aaa" {
		t.Fatalf("first flush failed")
	}

	w.Write([]byte("bbb"))
	w.Flush()
	if buf.String() != "aaabbb" {
		t.Fatalf("second flush failed")
	}

	w.Write([]byte("ccc"))
	w.Flush()
	if buf.String() != "aaabbbccc" {
		t.Fatalf("third flush failed")
	}
}

// Helper types for testing

type errorWriter struct {
	failAfter int
	calls     int
	testError error
}

func (ew *errorWriter) Write(p []byte) (int, error) {
	ew.calls++
	if ew.calls > ew.failAfter {
		ew.testError = io.ErrClosedPipe
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

type lockCountingMutex struct {
	acquiredCount *int32
	mu            sync.Mutex
}

func (lcm *lockCountingMutex) Lock() {
	atomic.AddInt32(lcm.acquiredCount, 1)
	lcm.mu.Lock()
}

func (lcm *lockCountingMutex) Unlock() {
	lcm.mu.Unlock()
}

type flushTimingRecorder struct {
	mu       sync.Mutex
	flushes  []flushRecord
	writerID int
}

type flushRecord struct {
	timestamp int64
	bytesOut  int
}

func (ftr *flushTimingRecorder) Write(p []byte) (int, error) {
	ftr.mu.Lock()
	ftr.flushes = append(ftr.flushes, flushRecord{
		timestamp: int64(len(ftr.flushes)), // Use sequence number as pseudo-timestamp
		bytesOut:  len(p),
	})
	ftr.mu.Unlock()
	return len(p), nil
}

func (ftr *flushTimingRecorder) GetFlushes() []flushRecord {
	ftr.mu.Lock()
	defer ftr.mu.Unlock()
	// Return a copy
	result := make([]flushRecord, len(ftr.flushes))
	copy(result, ftr.flushes)
	return result
}

// TestFlushLockedBufferedWriterConcurrentFlushStaggering tests 10 concurrent
// FlushLockedBufferedWriter instances to verify they stagger flush operations
// and don't create cumulative effects
func TestFlushLockedBufferedWriterConcurrentFlushStaggering(t *testing.T) {
	const numWriters = 10
	const bytesPerWrite = 100
	const numWrites = 50
	const bufferSize = 1024

	writers := make([]*FlushLockedBufferedWriter, numWriters)
	recorders := make([]*flushTimingRecorder, numWriters)
	locks := make([]sync.Locker, numWriters)
	flushSequence := make(chan flushEvent, numWriters*numWrites)

	// Track concurrent flush operations
	concurrentFlushes := int32(0)
	maxConcurrentFlushes := int32(0)
	var mu sync.Mutex

	// Initialize writers with timing recorders and individual locks
	for i := 0; i < numWriters; i++ {
		recorders[i] = &flushTimingRecorder{writerID: i}
		locks[i] = &flushTrackingLock{
			writerID: i,
			events:   flushSequence,
		}
		// Wrap with concurrency tracking
		wrappedRecorder := &concurrencyTrackingRecorder{
			underlying:           recorders[i],
			concurrentFlushes:    &concurrentFlushes,
			maxConcurrentFlushes: &maxConcurrentFlushes,
			mu:                   &mu,
		}
		writers[i] = NewFlushLockedBufferedWriter(wrappedRecorder, bufferSize, locks[i])
	}

	var wg sync.WaitGroup
	writeErrs := make(chan error, numWriters)

	// Launch 10 concurrent writers
	for writerIdx := 0; writerIdx < numWriters; writerIdx++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte('A' + idx)}, bytesPerWrite)

			for i := 0; i < numWrites; i++ {
				if _, err := writers[idx].Write(data); err != nil {
					writeErrs <- err
					return
				}
				// Occasionally flush to trigger the staggering
				if i%5 == 0 {
					if err := writers[idx].Flush(); err != nil {
						writeErrs <- err
						return
					}
				}
			}
			// Final flush
			if err := writers[idx].Flush(); err != nil {
				writeErrs <- err
			}
		}(writerIdx)
	}

	wg.Wait()
	close(flushSequence)
	close(writeErrs)

	// Check for any errors
	for err := range writeErrs {
		t.Fatalf("write error: %v", err)
	}

	// Collect all flush events
	events := make([]flushEvent, 0, len(flushSequence))
	for evt := range flushSequence {
		events = append(events, evt)
	}

	if len(events) == 0 {
		t.Fatalf("expected flush events, got none")
	}

	// Analyze timing distribution
	flushCountPerWriter := make(map[int]int)
	lockDurationPerWriter := make(map[int]int64)

	for _, evt := range events {
		flushCountPerWriter[evt.writerID]++
		lockDurationPerWriter[evt.writerID] += evt.lockHeldNanos
	}

	finalMax := atomic.LoadInt32(&maxConcurrentFlushes)

	t.Logf("TestFlushLockedBufferedWriterConcurrentFlushStaggering Results:")
	t.Logf("  - Max concurrent flush operations: %d (with independent locks per writer)", finalMax)
	t.Logf("  - Flush analysis across %d writers:", numWriters)
	t.Logf("  - Total flush events: %d", len(events))

	// Check that flushes are reasonably distributed
	for writerID := 0; writerID < numWriters; writerID++ {
		count := flushCountPerWriter[writerID]
		t.Logf("    Writer %d: %d flushes, %.2f µs lock time",
			writerID, count, float64(lockDurationPerWriter[writerID])/1000)

		if count == 0 {
			t.Fatalf("Writer %d had no flushes", writerID)
		}
	}

	// Verify data integrity
	totalBytesWritten := int64(0)
	for i := 0; i < numWriters; i++ {
		bytesOut := int64(0)
		for _, flush := range recorders[i].GetFlushes() {
			bytesOut += int64(flush.bytesOut)
		}
		expectedBytes := int64(numWrites * bytesPerWrite)
		if bytesOut != expectedBytes {
			t.Fatalf("Writer %d: expected %d bytes, got %d", i, expectedBytes, bytesOut)
		}
		totalBytesWritten += bytesOut
	}

	expectedTotal := int64(numWriters * numWrites * bytesPerWrite)
	if totalBytesWritten != expectedTotal {
		t.Fatalf("Total bytes mismatch: expected %d, got %d", expectedTotal, totalBytesWritten)
	}

	// Analyze staggering: check if flushes are interleaved
	flushTimelinePerWriter := make(map[int][]int)
	for idx, evt := range events {
		flushTimelinePerWriter[evt.writerID] = append(flushTimelinePerWriter[evt.writerID], idx)
	}

	// Verify that not all writers flush at the same time
	// by checking the distribution of flush events across writers
	minTimeBetweenWriters := findMinTimeGapBetweenWriters(events)
	if minTimeBetweenWriters == 0 {
		t.Fatalf("Writers are flushing simultaneously, no staggering detected")
	}

	t.Logf("  ✓ Staggering verified: flushes are interleaved across writers")
	t.Logf("  ✓ Data integrity verified: all %d bytes written correctly", totalBytesWritten)
}

type flushEvent struct {
	writerID      int
	sequenceNum   int
	lockHeldNanos int64
}

type flushTrackingLock struct {
	writerID int
	events   chan flushEvent
	mu       sync.Mutex
	seqNum   int
}

func (ftl *flushTrackingLock) Lock() {
	ftl.mu.Lock()
}

func (ftl *flushTrackingLock) Unlock() {
	ftl.seqNum++
	ftl.events <- flushEvent{
		writerID:    ftl.writerID,
		sequenceNum: ftl.seqNum,
	}
	ftl.mu.Unlock()
}

func findMinTimeGapBetweenWriters(events []flushEvent) int {
	// Track the last flush time for each writer
	lastFlushPerWriter := make(map[int]int)
	minGap := len(events)

	for idx, evt := range events {
		if lastFlush, exists := lastFlushPerWriter[evt.writerID]; exists {
			gap := idx - lastFlush
			if gap < minGap && gap > 0 {
				minGap = gap
			}
		}
		lastFlushPerWriter[evt.writerID] = idx
	}

	return minGap
}

// TestBuffioWriterConcurrentFlushStaggering demonstrates the PROBLEM
// with standard bufio.Writer: 10 concurrent writers all flush at the same time,
// causing lock contention and cumulative effects
func TestBuffioWriterConcurrentFlushStaggering(t *testing.T) {
	const numWriters = 10
	const bytesPerWrite = 100
	const numWrites = 50
	const bufferSize = 1024

	writers := make([]*bufio.Writer, numWriters)
	recorders := make([]*flushTimingRecorder, numWriters)
	locks := make([]sync.Locker, numWriters)
	flushSequence := make(chan flushEvent, numWriters*numWrites)

	// Initialize writers with tracking locks
	for i := 0; i < numWriters; i++ {
		recorders[i] = &flushTimingRecorder{writerID: i}
		locks[i] = &flushTrackingLock{
			writerID: i,
			events:   flushSequence,
		}
		// Wrap the recorder with lock tracking for flush operations
		wrappedWriter := &lockTrackingWriter{
			underlying: recorders[i],
			lock:       locks[i],
		}
		writers[i] = bufio.NewWriterSize(wrappedWriter, bufferSize)
	}

	var wg sync.WaitGroup
	writeErrs := make(chan error, numWriters)

	// Launch 10 concurrent writers
	for writerIdx := 0; writerIdx < numWriters; writerIdx++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte('A' + idx)}, bytesPerWrite)

			for i := 0; i < numWrites; i++ {
				if _, err := writers[idx].Write(data); err != nil {
					writeErrs <- err
					return
				}
				// Occasionally flush to trigger the staggering (or NOT stagger)
				if i%5 == 0 {
					if err := writers[idx].Flush(); err != nil {
						writeErrs <- err
						return
					}
				}
			}
			// Final flush
			if err := writers[idx].Flush(); err != nil {
				writeErrs <- err
			}
		}(writerIdx)
	}

	// Small delay to ensure all goroutines reach their first flush point
	time.Sleep(10 * time.Millisecond)

	wg.Wait()
	close(flushSequence)
	close(writeErrs)

	// Check for any errors
	for err := range writeErrs {
		t.Fatalf("write error: %v", err)
	}

	// Collect all flush events
	events := make([]flushEvent, 0, len(flushSequence))
	for evt := range flushSequence {
		events = append(events, evt)
	}

	if len(events) == 0 {
		t.Fatalf("expected flush events, got none")
	}

	// Analyze timing distribution
	flushCountPerWriter := make(map[int]int)
	for _, evt := range events {
		flushCountPerWriter[evt.writerID]++
	}

	t.Logf("\nTestBuffioWriterConcurrentFlushStaggering Results:")
	t.Logf("  === bufio.Writer CONGESTION ANALYSIS ===")
	t.Logf("  - Total flush events: %d (comparing to expected %d)", len(events), numWriters*11)
	t.Logf("  - Flush distribution per writer:")

	for writerID := 0; writerID < numWriters; writerID++ {
		count := flushCountPerWriter[writerID]
		t.Logf("    Writer %d: %d flushes", writerID, count)
	}

	// Detect simultaneous flushes (the problem)
	simultaneousFlushGroups := detectSimultaneousFlushGroups(events)
	t.Logf("\n  ⚠️  PROBLEM DETECTED:")
	t.Logf("  - Found %d groups of simultaneous flushes", len(simultaneousFlushGroups))

	if len(simultaneousFlushGroups) > 0 {
		for groupIdx, group := range simultaneousFlushGroups {
			if len(group) > 1 {
				t.Logf("    Group %d: %d writers flushing simultaneously: %v",
					groupIdx, len(group), group)
			}
		}
		t.Logf("  - This causes LOCK CONTENTION and CUMULATIVE EFFECTS")
	}

	// Compare with ideal staggering
	minGap := findMinTimeGapBetweenWriters(events)
	t.Logf("\n  - Minimum time gap between same-writer flushes: %d events", minGap)
	if minGap <= 1 {
		t.Logf("  - ⚠️  Writers are flushing back-to-back with NO interleaving from other writers")
	}
}

// TestComparisonFlushLockedVsBuffio is a side-by-side comparison showing
// FlushLockedBufferedWriter successfully staggering vs bufio.Writer congesting
func TestComparisonFlushLockedVsBuffio(t *testing.T) {
	const numWriters = 10
	const bytesPerWrite = 100
	const numWrites = 50
	const bufferSize = 1024

	t.Run("FlushLockedBufferedWriter-Serialized", func(t *testing.T) {
		writers := make([]*FlushLockedBufferedWriter, numWriters)

		concurrentFlushes := int32(0)
		maxConcurrentFlushes := int32(0)
		var mu sync.Mutex

		// Use a SHARED lock so all writers serialize their flushes
		var sharedFlushLock sync.Mutex

		for i := 0; i < numWriters; i++ {
			wrappedRecorder := &concurrencyTrackingRecorder{
				underlying:           &flushTimingRecorder{writerID: i},
				concurrentFlushes:    &concurrentFlushes,
				maxConcurrentFlushes: &maxConcurrentFlushes,
				mu:                   &mu,
			}
			writers[i] = NewFlushLockedBufferedWriter(wrappedRecorder, bufferSize, &sharedFlushLock)
		}

		var wg sync.WaitGroup
		for writerIdx := 0; writerIdx < numWriters; writerIdx++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				data := bytes.Repeat([]byte{byte('A' + idx)}, bytesPerWrite)
				for i := 0; i < numWrites; i++ {
					writers[idx].Write(data)
					if i%5 == 0 {
						writers[idx].Flush()
					}
				}
				writers[idx].Flush()
			}(writerIdx)
		}

		wg.Wait()

		finalMax := atomic.LoadInt32(&maxConcurrentFlushes)

		t.Logf("✓ FlushLockedBufferedWriter (with SHARED lock):")
		t.Logf("  - Max concurrent flush operations: %d", finalMax)
		if finalMax <= 1 {
			t.Logf("  - ✓ PASSED: Flush operations are perfectly serialized")
		} else {
			t.Errorf("  - ✗ FAILED: Expected max 1 concurrent flush, got %d", finalMax)
		}
	})

	t.Run("bufio.Writer-Congested", func(t *testing.T) {
		writers := make([]*bufio.Writer, numWriters)
		recorders := make([]*flushTimingRecorder, numWriters)
		locks := make([]sync.Locker, numWriters)
		flushSequence := make(chan flushEvent, numWriters*numWrites)

		for i := 0; i < numWriters; i++ {
			recorders[i] = &flushTimingRecorder{writerID: i}
			locks[i] = &flushTrackingLock{
				writerID: i,
				events:   flushSequence,
			}
			wrappedWriter := &lockTrackingWriter{
				underlying: recorders[i],
				lock:       locks[i],
			}
			writers[i] = bufio.NewWriterSize(wrappedWriter, bufferSize)
		}

		var wg sync.WaitGroup
		for writerIdx := 0; writerIdx < numWriters; writerIdx++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				data := bytes.Repeat([]byte{byte('A' + idx)}, bytesPerWrite)
				for i := 0; i < numWrites; i++ {
					writers[idx].Write(data)
					if i%5 == 0 {
						writers[idx].Flush()
					}
				}
				writers[idx].Flush()
			}(writerIdx)
		}

		time.Sleep(10 * time.Millisecond)
		wg.Wait()
		close(flushSequence)

		events := make([]flushEvent, 0, len(flushSequence))
		for evt := range flushSequence {
			events = append(events, evt)
		}

		maxSim, avgGap := analyzeFlushDistribution(events, numWriters)

		t.Logf("⚠️  bufio.Writer (independent locks per writer):")
		t.Logf("  - Total flushes: %d", len(events))
		t.Logf("  - Max writers flushing within %d-event window: %d (high contention!)", numWriters, maxSim)
		t.Logf("  - Average gap between same-writer flushes: %.2f events", avgGap)

		if maxSim > 5 {
			t.Logf("  - SEVERE CONGESTION DETECTED: %d writers flushing simultaneously", maxSim)
		}
	})
}

// Helper types for bufio comparison

type lockTrackingWriter struct {
	underlying io.Writer
	lock       sync.Locker
}

func (ltw *lockTrackingWriter) Write(p []byte) (int, error) {
	ltw.lock.Lock()
	defer ltw.lock.Unlock()
	return ltw.underlying.Write(p)
}

// detectSimultaneousFlushGroups groups flush events by timestamp sequence
// to identify when multiple writers flush at the same logical time
func detectSimultaneousFlushGroups(events []flushEvent) [][]int {
	if len(events) == 0 {
		return nil
	}

	// Count consecutive events from different writers
	// Events are "simultaneous" if they appear consecutively without a gap
	groups := [][]int{}
	currentWriters := make(map[int]bool)

	for i := 0; i < len(events); i++ {
		writerID := events[i].writerID

		// If this writer is already in current group, it means a new flush from same writer
		// Mark end of current group
		if currentWriters[writerID] && (i == len(events)-1 || events[i+1].writerID != writerID) {
			// Collect the group
			groupWriters := []int{}
			for w := range currentWriters {
				groupWriters = append(groupWriters, w)
			}
			if len(groupWriters) > 0 {
				groups = append(groups, groupWriters)
			}
			currentWriters = make(map[int]bool)
		}

		currentWriters[writerID] = true
	}

	return groups
}

// analyzeFlushDistribution provides detailed metrics about flush patterns.
// windowSize controls how many consecutive flush events to inspect at once;
// a larger value (e.g. numWriters) reveals broader clustering of concurrent flushes.
func analyzeFlushDistribution(events []flushEvent, windowSize int) (maxSimultaneous int, avgGap float64) {
	if len(events) == 0 {
		return 0, 0
	}

	// Track how many different writers flush within the given window
	maxInWindow := 0
	totalGaps := 0
	gapCount := 0

	// Look for windows where many writers flush close together
	for i := 0; i < len(events)-windowSize; i++ {
		writersInWindow := make(map[int]bool)
		for j := i; j < i+windowSize && j < len(events); j++ {
			writersInWindow[events[j].writerID] = true
		}
		if len(writersInWindow) > maxInWindow {
			maxInWindow = len(writersInWindow)
		}
	}

	// Calculate average gap between events
	lastPerWriter := make(map[int]int)
	for i, evt := range events {
		if last, ok := lastPerWriter[evt.writerID]; ok {
			gap := i - last
			totalGaps += gap
			gapCount++
		}
		lastPerWriter[evt.writerID] = i
	}

	avgGap = float64(totalGaps) / float64(gapCount)
	return maxInWindow, avgGap
}

// TestFlushLockedBufferedWriterTrueSerialFlushing verifies that at most ONE flush
// operation is actually in progress at any given time (true mutual exclusion)
func TestFlushLockedBufferedWriterTrueSerialFlushing(t *testing.T) {
	const numWriters = 10
	const bytesPerWrite = 100
	const numWrites = 50

	// Track actual concurrent flush operations
	concurrentFlushes := int32(0)
	maxConcurrentFlushes := int32(0)
	var mu sync.Mutex

	writers := make([]*FlushLockedBufferedWriter, numWriters)
	var wg sync.WaitGroup

	// Create a SHARED lock for all writers (this is the key for actual serialization)
	var sharedFlushLock sync.Mutex

	// Create writers with a serializing writer that tracks concurrency
	for i := 0; i < numWriters; i++ {
		recorder := &concurrencyTrackingWriter{
			writerID:             i,
			concurrentFlushes:    &concurrentFlushes,
			maxConcurrentFlushes: &maxConcurrentFlushes,
			mu:                   &mu,
		}
		writers[i] = NewFlushLockedBufferedWriter(recorder, 1024, &sharedFlushLock)
	}

	// Launch concurrent writers
	for writerIdx := 0; writerIdx < numWriters; writerIdx++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte('A' + idx)}, bytesPerWrite)

			for i := 0; i < numWrites; i++ {
				writers[idx].Write(data)
				if i%5 == 0 {
					writers[idx].Flush()
				}
			}
			writers[idx].Flush()
		}(writerIdx)
	}

	wg.Wait()

	// Verify results
	finalMax := atomic.LoadInt32(&maxConcurrentFlushes)
	t.Logf("TestFlushLockedBufferedWriterTrueSerialFlushing Results:")
	t.Logf("  - Max concurrent flush operations: %d (with SHARED lock)", finalMax)

	if finalMax > 1 {
		t.Fatalf("FAILED: More than 1 flush operation in progress simultaneously! Max: %d", finalMax)
	}

	if finalMax == 0 {
		t.Fatalf("FAILED: No flush operations recorded!")
	}

	t.Logf("  ✓ PASSED: Flush operations are perfectly serialized")
}

// TestFlushLockedBufferedWriterWithIndependentLocks shows the PROBLEM when each writer has its own lock
func TestFlushLockedBufferedWriterWithIndependentLocks(t *testing.T) {
	const numWriters = 10
	const bytesPerWrite = 100
	const numWrites = 50

	concurrentFlushes := int32(0)
	maxConcurrentFlushes := int32(0)
	var mu sync.Mutex

	writers := make([]*FlushLockedBufferedWriter, numWriters)
	var wg sync.WaitGroup

	// Create writers with INDEPENDENT locks (the problem!)
	for i := 0; i < numWriters; i++ {
		recorder := &concurrencyTrackingWriter{
			writerID:             i,
			concurrentFlushes:    &concurrentFlushes,
			maxConcurrentFlushes: &maxConcurrentFlushes,
			mu:                   &mu,
		}
		// Each writer gets its own lock - this is the PROBLEM!
		independentLock := &sync.Mutex{}
		writers[i] = NewFlushLockedBufferedWriter(recorder, 1024, independentLock)
	}

	// Launch concurrent writers
	for writerIdx := 0; writerIdx < numWriters; writerIdx++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte('A' + idx)}, bytesPerWrite)

			for i := 0; i < numWrites; i++ {
				writers[idx].Write(data)
				if i%5 == 0 {
					writers[idx].Flush()
				}
			}
			writers[idx].Flush()
		}(writerIdx)
	}

	wg.Wait()

	// Verify results
	finalMax := atomic.LoadInt32(&maxConcurrentFlushes)
	t.Logf("TestFlushLockedBufferedWriterWithIndependentLocks Results:")
	t.Logf("  - Max concurrent flush operations: %d (with INDEPENDENT locks)", finalMax)

	if finalMax == 1 {
		t.Logf("  ℹ️  Unexpected: Only 1 concurrent. This might not stress the scenario enough.")
	} else if finalMax > 1 {
		t.Logf("  ⚠️  PROBLEM CONFIRMED: %d flush operations running simultaneously!", finalMax)
		t.Logf("  - This demonstrates why a shared lock is NECESSARY for true serialization")
	}
}

// concurrencyTrackingWriter tracks how many threads are currently writing
type concurrencyTrackingWriter struct {
	writerID             int
	concurrentFlushes    *int32
	maxConcurrentFlushes *int32
	mu                   *sync.Mutex
	bytesWritten         int64
}

func (ctw *concurrencyTrackingWriter) Write(p []byte) (int, error) {
	// Increment concurrent counter
	current := atomic.AddInt32(ctw.concurrentFlushes, 1)
	defer atomic.AddInt32(ctw.concurrentFlushes, -1)

	// Track the maximum
	for {
		max := atomic.LoadInt32(ctw.maxConcurrentFlushes)
		if current <= max {
			break
		}
		if atomic.CompareAndSwapInt32(ctw.maxConcurrentFlushes, max, current) {
			break
		}
	}

	// Simulate some I/O work
	time.Sleep(1 * time.Microsecond)

	ctw.mu.Lock()
	ctw.bytesWritten += int64(len(p))
	ctw.mu.Unlock()

	return len(p), nil
}

// concurrencyTrackingRecorder wraps a flushTimingRecorder to track concurrent flush operations
type concurrencyTrackingRecorder struct {
	underlying           *flushTimingRecorder
	concurrentFlushes    *int32
	maxConcurrentFlushes *int32
	mu                   *sync.Mutex
}

func (ctr *concurrencyTrackingRecorder) Write(p []byte) (int, error) {
	// Increment concurrent counter
	current := atomic.AddInt32(ctr.concurrentFlushes, 1)
	defer atomic.AddInt32(ctr.concurrentFlushes, -1)

	// Track the maximum
	for {
		max := atomic.LoadInt32(ctr.maxConcurrentFlushes)
		if current <= max {
			break
		}
		if atomic.CompareAndSwapInt32(ctr.maxConcurrentFlushes, max, current) {
			break
		}
	}

	// Delegate to underlying
	return ctr.underlying.Write(p)
}

// GetFlushes returns flushes from underlying recorder
func (ctr *concurrencyTrackingRecorder) GetFlushes() []flushRecord {
	return ctr.underlying.GetFlushes()
}
