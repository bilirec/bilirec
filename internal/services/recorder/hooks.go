package recorder

import (
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/processors"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
)

func (r *Service) pipelineHooks(roomID int, info *Info) rs.PipelineHooks {
	sess := r.m.StreamSession(roomID, metrics.StreamSessionMeta{
		Streamer:  info.room.Uname,
		RoomTitle: info.room.Title,
	})

	return rs.PipelineHooks{
		OnBytesWritten: func(n int) {
			info.bytesWritten.Add(uint64(n))
			sess.AddBytesWritten(n)
		},
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
