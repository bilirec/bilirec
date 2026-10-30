package metrics

import (
	"math"
	"time"

	vm "github.com/VictoriaMetrics/metrics"
)

// Pipeline observations only. Recorder state transitions stay in metrics_recorder.go.
const (
	metricRoomStreamBytesTotal                = "bilirec_room_stream_bytes_total"
	metricRoomStreamBytesWrittenTotal         = "bilirec_room_stream_bytes_written_total"
	metricRoomStreamTimestampJumpsTotal       = "bilirec_room_stream_timestamp_jumps_total"
	metricRoomStreamTimestampJumpCollapsedSec = "bilirec_room_stream_timestamp_jump_collapsed_seconds_total"
	metricRoomStreamEnqueueWaitsTotal         = "bilirec_room_stream_enqueue_waits_total"
	metricRoomStreamEnqueueWaitSecondsTotal   = "bilirec_room_stream_enqueue_wait_seconds_total"
	metricRoomStreamSlowFlushesTotal          = "bilirec_room_stream_slow_flushes_total"
	metricRoomStreamSlowSyncsTotal            = "bilirec_room_stream_slow_syncs_total"
)

// Current StreamObserver implementations stay on the synchronous contract
// documented on that interface. recorder statsObserver only atomic-adds.
// streamMetricsObserver only increments counters bound at construction.
// notify streamNotifyObserver returns immediately from the byte callbacks.
// Enqueue waits reach it only after StreamObservation drops waits under
// MinEnqueueWaitRecord (10ms). Timeline gaps, slow flushes, slow syncs, and
// those waits take the metricsAlerts mutex for an O(1) bucket update.
// publishPayload sends the push on its own goroutines.

// streamMetricsObserver records one room's session counters.
type streamMetricsObserver struct {
	bytesRead              *vm.Counter
	bytesWritten           *vm.Counter
	timestampJumps         *vm.Counter
	timestampJumpCollapsed *vm.FloatCounter
	enqueueWaits           *vm.Counter
	enqueueWaitSeconds     *vm.FloatCounter
	slowFlushes            *vm.Counter
	slowSyncs              *vm.Counter
}

func newStreamMetricsObserver(reg *roomRegistry, roomID int) *streamMetricsObserver {
	return &streamMetricsObserver{
		bytesRead:              reg.counter(metricRoomStreamBytesTotal, roomID),
		bytesWritten:           reg.counter(metricRoomStreamBytesWrittenTotal, roomID),
		timestampJumps:         reg.counter(metricRoomStreamTimestampJumpsTotal, roomID),
		timestampJumpCollapsed: reg.floatCounter(metricRoomStreamTimestampJumpCollapsedSec, roomID),
		enqueueWaits:           reg.counter(metricRoomStreamEnqueueWaitsTotal, roomID),
		enqueueWaitSeconds:     reg.floatCounter(metricRoomStreamEnqueueWaitSecondsTotal, roomID),
		slowFlushes:            reg.counter(metricRoomStreamSlowFlushesTotal, roomID),
		slowSyncs:              reg.counter(metricRoomStreamSlowSyncsTotal, roomID),
	}
}

func (o *streamMetricsObserver) OnBytesRead(_ int, _ StreamObservationMeta, n int) {
	if n <= 0 || o.bytesRead == nil {
		return
	}
	o.bytesRead.Add(n)
}

func (o *streamMetricsObserver) OnBytesWritten(_ int, _ StreamObservationMeta, n int) {
	if n <= 0 || o.bytesWritten == nil {
		return
	}
	o.bytesWritten.Add(n)
}

func (o *streamMetricsObserver) OnTimelineGap(_ int, _ StreamObservationMeta, delta time.Duration) {
	if o.timestampJumps == nil || delta <= 0 {
		return
	}
	ms := delta.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	if ms > math.MaxInt32 {
		ms = math.MaxInt32
	}
	o.timestampJumps.Inc()
	o.timestampJumpCollapsed.Add(float64(ms) / 1000)
}

func (o *streamMetricsObserver) OnFlush(_ int, _ StreamObservationMeta, duration time.Duration) {
	if duration <= SlowFlushMetricThreshold || o.slowFlushes == nil {
		return
	}
	o.slowFlushes.Inc()
}

func (o *streamMetricsObserver) OnSync(_ int, _ StreamObservationMeta, duration time.Duration) {
	if duration <= SlowSyncMetricThreshold || o.slowSyncs == nil {
		return
	}
	o.slowSyncs.Inc()
}

func (o *streamMetricsObserver) OnEnqueueWait(_ int, _ StreamObservationMeta, wait time.Duration) {
	if o.enqueueWaits == nil {
		return
	}
	o.enqueueWaits.Inc()
	o.enqueueWaitSeconds.Add(wait.Seconds())
}

func (e *Exporter) unregisterStreamSession(roomID int) {
	if e.registry == nil {
		return
	}
	e.registry.unregisterCounter(metricRoomStreamBytesTotal, roomID)
	e.registry.unregisterCounter(metricRoomStreamBytesWrittenTotal, roomID)
	e.registry.unregisterCounter(metricRoomStreamTimestampJumpsTotal, roomID)
	e.registry.unregisterFloatCounter(metricRoomStreamTimestampJumpCollapsedSec, roomID)
	e.registry.unregisterCounter(metricRoomStreamEnqueueWaitsTotal, roomID)
	e.registry.unregisterFloatCounter(metricRoomStreamEnqueueWaitSecondsTotal, roomID)
	e.registry.unregisterCounter(metricRoomStreamSlowFlushesTotal, roomID)
	e.registry.unregisterCounter(metricRoomStreamSlowSyncsTotal, roomID)
}
