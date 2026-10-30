package recorder

import (
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/internal/modules/metrics"
)

// statsObserver keeps per-session totals. Each callback is an atomic add and
// returns immediately. See metrics.StreamObserver.
type statsObserver struct {
	BytesRead     atomic.Uint64
	BytesWritten  atomic.Uint64
	FlushCount    atomic.Uint64
	FlushNs       atomic.Uint64
	SyncCount     atomic.Uint64
	SyncNs        atomic.Uint64
	EnqueueWaits  atomic.Uint64
	EnqueueWaitNs atomic.Uint64
	TimelineGaps  atomic.Uint64
	TimelineGapNs atomic.Uint64
}

func (o *statsObserver) OnBytesRead(_ int, _ metrics.StreamObservationMeta, n int) {
	if n <= 0 {
		return
	}
	o.BytesRead.Add(uint64(n))
}

func (o *statsObserver) OnBytesWritten(_ int, _ metrics.StreamObservationMeta, n int) {
	if n <= 0 {
		return
	}
	o.BytesWritten.Add(uint64(n))
}

func (o *statsObserver) OnTimelineGap(_ int, _ metrics.StreamObservationMeta, delta time.Duration) {
	if delta <= 0 {
		return
	}
	o.TimelineGaps.Add(1)
	o.TimelineGapNs.Add(uint64(delta))
}

func (o *statsObserver) OnFlush(_ int, _ metrics.StreamObservationMeta, duration time.Duration) {
	if duration <= 0 {
		return
	}
	o.FlushCount.Add(1)
	o.FlushNs.Add(uint64(duration))
}

func (o *statsObserver) OnSync(_ int, _ metrics.StreamObservationMeta, duration time.Duration) {
	if duration <= 0 {
		return
	}
	o.SyncCount.Add(1)
	o.SyncNs.Add(uint64(duration))
}

func (o *statsObserver) OnEnqueueWait(_ int, _ metrics.StreamObservationMeta, wait time.Duration) {
	if wait <= 0 {
		return
	}
	o.EnqueueWaits.Add(1)
	o.EnqueueWaitNs.Add(uint64(wait))
}
