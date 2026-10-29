package recorder

import (
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/processors"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
)

func (r *Service) pipelineHooks(sess *metrics.StreamObservation) rs.PipelineHooks {
	return rs.PipelineHooks{
		OnBytesWritten: sess.AddBytesWritten,
		OnFlush: func(ev processors.FlushEvent) {
			sess.RecordFlush(ev.Duration)
		},
		OnSync: func(ev processors.SyncEvent) {
			sess.RecordSync(ev.Duration)
		},
		OnEnqueueWait: func(ev processors.EnqueueWaitEvent) {
			sess.RecordEnqueueWait(ev.Duration)
		},
		OnTimelineGap: func(g processors.TimelineGap) {
			sess.RecordTimelineGap(g.Delta)
		},
	}
}
