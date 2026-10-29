package recorder

import (
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/processors"
)

func TestPipelineHooks_MetricsDisabled(t *testing.T) {
	r := &Service{m: &metrics.Exporter{}}
	stats := &statsObserver{}
	meta := metrics.StreamObservationMeta{}
	obs := r.m.StreamObservation(123, meta, stats)
	info := &Info{
		room:      &bilibili.LiveRoomInfoDetail{},
		stats:     stats,
		streamObs: obs,
	}
	hooks := r.pipelineHooks(obs)

	if hooks.OnFlush == nil || hooks.OnSync == nil || hooks.OnEnqueueWait == nil || hooks.OnTimelineGap == nil {
		t.Fatal("pipeline hooks must always be wired; StreamObservation no-ops VM when disabled")
	}
	if hooks.OnBytesWritten == nil {
		t.Fatal("OnBytesWritten must always be set for stats")
	}

	hooks.OnBytesWritten(4096)
	if got := info.stats.BytesWritten.Load(); got != 4096 {
		t.Fatalf("expected bytesWritten 4096, got %d", got)
	}

	hooks.OnFlush(processors.FlushEvent{Duration: 4 * time.Second})
	hooks.OnSync(processors.SyncEvent{Duration: 2 * time.Second})
	hooks.OnEnqueueWait(processors.EnqueueWaitEvent{Duration: 50 * time.Millisecond})
	hooks.OnTimelineGap(processors.TimelineGap{Delta: time.Second})

	if info.stats.FlushCount.Load() != 1 || info.stats.FlushNs.Load() != uint64(4*time.Second) {
		t.Fatalf("flush stats: count=%d ns=%d", info.stats.FlushCount.Load(), info.stats.FlushNs.Load())
	}
	if info.stats.SyncCount.Load() != 1 {
		t.Fatalf("sync count = %d", info.stats.SyncCount.Load())
	}
	if info.stats.EnqueueWaits.Load() != 1 {
		t.Fatalf("enqueue waits = %d", info.stats.EnqueueWaits.Load())
	}
	if info.stats.TimelineGaps.Load() != 1 {
		t.Fatalf("timeline gaps = %d", info.stats.TimelineGaps.Load())
	}
}
