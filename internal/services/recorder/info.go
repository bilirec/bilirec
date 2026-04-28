package recorder

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
)

type Info struct {
	status     atomic.Pointer[RecordStatus]
	bytesRead  atomic.Uint64
	startTime  time.Time
	outputPath atomic.Value // string

	cancel context.CancelFunc
	room   *bilibili.LiveRoomInfoDetail
}

func (r *Info) SetOutputPath(path string) {
	r.outputPath.Store(path) // must always store string
}

func (r *Info) OutputPath() string {
	v := r.outputPath.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}
