package record_strategies

import (
	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/flv"
)

// PipelineHooks are session-level observers applied when a segment pipeline is built.
// Writer callbacks apply to every format. OnTimestampJump is used by FLV only.
type PipelineHooks struct {
	OnBytesWritten  func(n int)
	OnFlush         func(processors.FlushEvent)
	OnSync          func(processors.SyncEvent)
	OnEnqueueWait   func(processors.EnqueueWaitEvent)
	OnTimestampJump flv.TimestampJumpReporter
}
