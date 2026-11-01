package metrics

import (
	"math"
	"time"

	"github.com/bilirec/bilirec/internal/services/notify"
	vm "github.com/VictoriaMetrics/metrics"
)

const (
	minEnqueueWaitRecord     = 10 * time.Millisecond
	slowFlushMetricThreshold = 3 * time.Second
	slowSyncMetricThreshold  = 1200 * time.Millisecond
)

// StreamSession holds per-room VictoriaMetrics counters for pipeline observers
// on one recording connection. All handles are resolved once when created.
type StreamSession struct {
	roomID              int
	notify              *notify.Service
	notifyMetricsAlerts bool

	bytesWritten           *vm.Counter
	timestampJumps         *vm.Counter
	timestampJumpCollapsed *vm.FloatCounter
	enqueueWaits           *vm.Counter
	enqueueWaitSeconds     *vm.FloatCounter
	slowFlushes            *vm.Counter
	slowSyncs              *vm.Counter
}

// StreamSession returns counter handles for one room when metrics export is enabled.
func (e *Exporter) StreamSession(roomID int) *StreamSession {
	sess := &StreamSession{
		roomID:              roomID,
		notify:              e.notify,
		notifyMetricsAlerts: e.notifyMetricsAlerts,
	}
	if e.registry == nil {
		return sess
	}
	sess.bytesWritten = e.registry.counter(metricRoomStreamBytesWrittenTotal, roomID)
	sess.timestampJumps = e.registry.counter(metricRoomStreamTimestampJumpsTotal, roomID)
	sess.timestampJumpCollapsed = e.registry.floatCounter(metricRoomStreamTimestampJumpCollapsedSec, roomID)
	sess.enqueueWaits = e.registry.counter(metricRoomStreamEnqueueWaitsTotal, roomID)
	sess.enqueueWaitSeconds = e.registry.floatCounter(metricRoomStreamEnqueueWaitSecondsTotal, roomID)
	sess.slowFlushes = e.registry.counter(metricRoomStreamSlowFlushesTotal, roomID)
	sess.slowSyncs = e.registry.counter(metricRoomStreamSlowSyncsTotal, roomID)
	return sess
}

// RecordTimelineGap records a positive media timeline discontinuity to metrics and optional notify.
func (s *StreamSession) RecordTimelineGap(delta time.Duration) {
	if delta <= 0 {
		return
	}
	if s.timestampJumps != nil {
		ms := delta.Milliseconds()
		if ms <= 0 {
			ms = 1
		}
		if ms > math.MaxInt32 {
			ms = math.MaxInt32
		}
		s.timestampJumps.Inc()
		s.timestampJumpCollapsed.Add(float64(ms) / 1000)
	}
	if s.notifyMetricsAlerts {
		s.notify.ObserveRecordingTimelineGap(s.roomID, delta)
	}
}

// AddBytesWritten accumulates bytes passed to the segment writer Write().
func (s *StreamSession) AddBytesWritten(n int) {
	if n <= 0 || s.bytesWritten == nil {
		return
	}
	s.bytesWritten.Add(n)
}

// RecordFlush records periodic or close flushes; slow flushes increment metrics and may notify.
func (s *StreamSession) RecordFlush(duration time.Duration) {
	if duration <= slowFlushMetricThreshold {
		return
	}
	if s.slowFlushes != nil {
		s.slowFlushes.Inc()
	}
	if s.notifyMetricsAlerts {
		s.notify.ObserveRecordingSlowFlush(s.roomID)
	}
}

// RecordSync records fsync timing; slow syncs increment metrics and may notify.
func (s *StreamSession) RecordSync(duration time.Duration) {
	if duration <= slowSyncMetricThreshold {
		return
	}
	if s.slowSyncs != nil {
		s.slowSyncs.Inc()
	}
	if s.notifyMetricsAlerts {
		s.notify.ObserveRecordingSlowSync(s.roomID)
	}
}

// RecordEnqueueWait records backpressure while the writer queue was full.
func (s *StreamSession) RecordEnqueueWait(wait time.Duration) {
	if wait < minEnqueueWaitRecord {
		return
	}
	if s.enqueueWaits != nil {
		recordEnqueueWait(s.enqueueWaits, s.enqueueWaitSeconds, wait)
	}
	if s.notifyMetricsAlerts {
		s.notify.ObserveRecordingEnqueueWait(s.roomID, wait)
	}
}

func recordEnqueueWait(waits *vm.Counter, waitSec *vm.FloatCounter, wait time.Duration) {
	waits.Inc()
	waitSec.Add(wait.Seconds())
}
