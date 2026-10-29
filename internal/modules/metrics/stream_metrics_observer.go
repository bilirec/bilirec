package metrics

import (
	"math"
	"time"

	vm "github.com/VictoriaMetrics/metrics"
)

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
