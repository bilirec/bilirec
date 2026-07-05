package recorder

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/ds"
)

type Info struct {
	status      atomic.Pointer[RecordStatus]
	bytesRead   atomic.Uint64
	actualQn    atomic.Int32
	isAudioOnly atomic.Bool
	startTime   time.Time
	outputPath  ds.Atomic[string]
	maxDuration time.Duration // internal runtime semantics: 0 = unlimited

	ctx          context.Context
	cancel       context.CancelFunc
	startOptions RecordStartOptions
	room         *bilibili.LiveRoomInfoDetail
	backoff      backoff.Backoff
}

func (r *Info) SetStream(qn int, audioOnly bool) {
	r.actualQn.Store(int32(qn))
	r.isAudioOnly.Store(audioOnly)
}

func (r *Info) ActualQn() int {
	return int(r.actualQn.Load())
}

func (r *Info) IsAudioOnly() bool {
	return r.isAudioOnly.Load()
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
