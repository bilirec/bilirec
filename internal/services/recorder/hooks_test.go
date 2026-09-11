package recorder

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/metrics"
)

func TestPipelineHooks_MetricsDisabled(t *testing.T) {
	r := &Service{m: &metrics.Exporter{}}
	info := &Info{}
	hooks := r.pipelineHooks(123, info)

	if hooks.OnFlush != nil || hooks.OnSync != nil || hooks.OnEnqueueWait != nil || hooks.OnTimestampJump != nil {
		t.Fatal("metric observers must be nil when exporter is disabled")
	}
	if hooks.OnBytesWritten == nil {
		t.Fatal("OnBytesWritten must always be set for stats")
	}

	hooks.OnBytesWritten(4096)
	if got := info.bytesWritten.Load(); got != 4096 {
		t.Fatalf("expected bytesWritten 4096, got %d", got)
	}
}
