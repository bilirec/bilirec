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
	info := &Info{room: &bilibili.LiveRoomInfoDetail{}}
	hooks := r.pipelineHooks(123, info)

	if hooks.OnFlush == nil || hooks.OnSync == nil || hooks.OnEnqueueWait == nil || hooks.OnTimelineGap == nil {
		t.Fatal("pipeline hooks must always be wired; StreamObservation no-ops VM when disabled")
	}
	if hooks.OnBytesWritten == nil {
		t.Fatal("OnBytesWritten must always be set for stats")
	}

	hooks.OnBytesWritten(4096)
	if got := info.bytesWritten.Load(); got != 4096 {
		t.Fatalf("expected bytesWritten 4096, got %d", got)
	}

	hooks.OnFlush(processors.FlushEvent{Duration: 4 * time.Second})
	hooks.OnSync(processors.SyncEvent{Duration: 2 * time.Second})
	hooks.OnEnqueueWait(processors.EnqueueWaitEvent{Duration: 50 * time.Millisecond})
	hooks.OnTimelineGap(processors.TimelineGap{Delta: time.Second})
}
