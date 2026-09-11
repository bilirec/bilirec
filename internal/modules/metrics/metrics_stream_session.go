package metrics

import (
	"time"

	vm "github.com/VictoriaMetrics/metrics"
)

const minEnqueueWaitRecord = 10 * time.Millisecond

// StreamSession holds per-room VictoriaMetrics counters for pipeline observers
// on one recording connection. All handles are resolved once when created.
type StreamSession struct {
	bytesWritten           *vm.Counter
	timestampJumps         *vm.Counter
	timestampJumpCollapsed *vm.FloatCounter
	enqueueWaits           *vm.Counter
	enqueueWaitSeconds     *vm.FloatCounter
	slowFlushes            *vm.Counter
	slowSyncs              *vm.Counter
}

// StreamSession returns eagerly initialized counter handles for one room's stream metrics.
func (e *Exporter) StreamSession(roomID int) *StreamSession {
	if e.registry == nil {
		return nil
	}
	return &StreamSession{
		bytesWritten:           e.registry.counter(metricRoomStreamBytesWrittenTotal, roomID),
		timestampJumps:         e.registry.counter(metricRoomStreamTimestampJumpsTotal, roomID),
		timestampJumpCollapsed: e.registry.floatCounter(metricRoomStreamTimestampJumpCollapsedSec, roomID),
		enqueueWaits:           e.registry.counter(metricRoomStreamEnqueueWaitsTotal, roomID),
		enqueueWaitSeconds:     e.registry.floatCounter(metricRoomStreamEnqueueWaitSecondsTotal, roomID),
		slowFlushes:            e.registry.counter(metricRoomStreamSlowFlushesTotal, roomID),
		slowSyncs:              e.registry.counter(metricRoomStreamSlowSyncsTotal, roomID),
	}
}

// AddBytesWritten accumulates bytes passed to the segment writer Write().
func (s *StreamSession) AddBytesWritten(n int) {
	if n <= 0 {
		return
	}
	s.bytesWritten.Add(n)
}

// RecordTimestampJump records a positive FLV timestamp jump.
func (s *StreamSession) RecordTimestampJump(deltaMs int32) {
	if deltaMs <= 0 {
		return
	}
	s.timestampJumps.Inc()
	s.timestampJumpCollapsed.Add(float64(deltaMs) / 1000)
}

// RecordEnqueueWait records backpressure while the writer queue was full.
func (s *StreamSession) RecordEnqueueWait(wait time.Duration) {
	recordEnqueueWait(s.enqueueWaits, s.enqueueWaitSeconds, wait)
}

func recordEnqueueWait(waits *vm.Counter, waitSec *vm.FloatCounter, wait time.Duration) {
	if wait < minEnqueueWaitRecord {
		return
	}
	waits.Inc()
	waitSec.Add(wait.Seconds())
}

// RecordSlowFlush records a flush slower than the WARN threshold.
func (s *StreamSession) RecordSlowFlush() {
	s.slowFlushes.Inc()
}

// RecordSlowSync records an fsync slower than the WARN threshold.
func (s *StreamSession) RecordSlowSync() {
	s.slowSyncs.Inc()
}
