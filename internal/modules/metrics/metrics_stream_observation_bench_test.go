package metrics

import (
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkStreamObservationFanOut times one chunk through AddBytesRead and
// AddBytesWritten. The attached observers match the production fan-out:
// streamMetricsObserver, the atomic byte path of recorder.statsObserver, and
// the no-op byte path of notify.streamNotifyObserver. Those two types are
// unexported in other packages, so this file repeats their byte-callback cost.
func BenchmarkStreamObservationFanOut(b *testing.B) {
	const chunk = 32 << 10

	reg := newRoomRegistry()
	metricsObs := newStreamMetricsObserver(reg, 1)
	stats := &benchStatsObserver{}
	notify := benchNotifyObserver{}
	obs := &StreamObservation{
		roomID: 1,
		meta:   StreamObservationMeta{Streamer: "bench", RoomTitle: "bench"},
		observers: []StreamObserver{
			metricsObs,
			stats,
			notify,
		},
	}

	obs.AddBytesRead(chunk)
	obs.AddBytesWritten(chunk)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		obs.AddBytesRead(chunk)
		obs.AddBytesWritten(chunk)
	}
}

// benchStatsObserver repeats recorder.statsObserver's byte callbacks.
type benchStatsObserver struct {
	bytesRead    atomic.Uint64
	bytesWritten atomic.Uint64
}

func (o *benchStatsObserver) OnBytesRead(_ int, _ StreamObservationMeta, n int) {
	if n <= 0 {
		return
	}
	o.bytesRead.Add(uint64(n))
}

func (o *benchStatsObserver) OnBytesWritten(_ int, _ StreamObservationMeta, n int) {
	if n <= 0 {
		return
	}
	o.bytesWritten.Add(uint64(n))
}

func (o *benchStatsObserver) OnTimelineGap(int, StreamObservationMeta, time.Duration) {}

func (o *benchStatsObserver) OnFlush(int, StreamObservationMeta, time.Duration) {}

func (o *benchStatsObserver) OnSync(int, StreamObservationMeta, time.Duration) {}

func (o *benchStatsObserver) OnEnqueueWait(int, StreamObservationMeta, time.Duration) {}

// benchNotifyObserver repeats notify.streamNotifyObserver's byte callbacks.
type benchNotifyObserver struct{}

func (benchNotifyObserver) OnBytesRead(int, StreamObservationMeta, int) {}

func (benchNotifyObserver) OnBytesWritten(int, StreamObservationMeta, int) {}

func (benchNotifyObserver) OnTimelineGap(int, StreamObservationMeta, time.Duration) {}

func (benchNotifyObserver) OnFlush(int, StreamObservationMeta, time.Duration) {}

func (benchNotifyObserver) OnSync(int, StreamObservationMeta, time.Duration) {}

func (benchNotifyObserver) OnEnqueueWait(int, StreamObservationMeta, time.Duration) {}
