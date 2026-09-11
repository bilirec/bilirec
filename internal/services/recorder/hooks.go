package recorder

import (
	"time"

	"github.com/bilirec/bilirec/internal/processors"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/pkg/flv"
)

const (
	slowFlushMetricThreshold = 3 * time.Second
	slowSyncMetricThreshold  = 1200 * time.Millisecond
)

func (r *Service) pipelineHooks(roomID int, info *Info) rs.PipelineHooks {
	if !r.m.Enabled() {
		return rs.PipelineHooks{
			OnBytesWritten: func(n int) {
				info.bytesWritten.Add(uint64(n))
			},
		}
	}

	var sess = r.m.StreamSession(roomID)

	return rs.PipelineHooks{
		OnBytesWritten: func(n int) {
			info.bytesWritten.Add(uint64(n))
			sess.AddBytesWritten(n)
		},
		OnFlush: func(ev processors.FlushEvent) {
			if ev.Duration > slowFlushMetricThreshold {
				sess.RecordSlowFlush()
			}
		},
		OnSync: func(ev processors.SyncEvent) {
			if ev.Duration > slowSyncMetricThreshold {
				sess.RecordSlowSync()
			}
		},
		OnEnqueueWait: func(ev processors.EnqueueWaitEvent) {
			sess.RecordEnqueueWait(ev.Duration)
		},
		OnTimestampJump: func(w flv.TimestampJumpWarning) {
			if w.Delta > 0 {
				sess.RecordTimestampJump(w.Delta)
			}
		},
	}
}
