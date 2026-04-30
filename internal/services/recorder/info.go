package recorder

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/pkg/ds"
)

type Info struct {
	status     atomic.Pointer[RecordStatus]
	bytesRead  atomic.Uint64
	startTime  time.Time
	outputPath ds.Atomic[string]

	cancel context.CancelFunc
	room   *bilibili.LiveRoomInfoDetail
}

func (r *Info) SetOutputPath(path string) {
	r.outputPath.Store(path)
}

func (r *Info) OutputPath() string {
	v, ok := r.outputPath.Load()
	if !ok {
		return ""
	}
	return v
}
