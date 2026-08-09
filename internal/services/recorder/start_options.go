package recorder

import (
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

type RecordStartOptions struct {
	hasDuration   bool
	duration      time.Duration
	streamOptions []bilibili.GetStreamURLsOption
	recordDanmaku bool
}

type RecordStartOption func(*RecordStartOptions)

func newRecordStartOptions() RecordStartOptions {
	return RecordStartOptions{}
}

func WithDuration(d time.Duration) RecordStartOption {
	return func(o *RecordStartOptions) {
		// Internal recorder semantics: d == 0 means unlimited.
		o.hasDuration = true
		o.duration = d
	}
}

func WithStreamOptions(opts ...bilibili.GetStreamURLsOption) RecordStartOption {
	return func(o *RecordStartOptions) {
		o.streamOptions = opts
	}
}

// WithRecordDanmaku enables or disables live-chat sidecar recording for this start.
// Default is false when the option is omitted.
func WithRecordDanmaku(enabled bool) RecordStartOption {
	return func(o *RecordStartOptions) {
		o.recordDanmaku = enabled
	}
}

func snapshotStartOptions(o RecordStartOptions) RecordStartOptions {
	out := o
	if len(o.streamOptions) > 0 {
		out.streamOptions = append([]bilibili.GetStreamURLsOption(nil), o.streamOptions...)
	}
	return out
}
