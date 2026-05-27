package recorder

import (
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

type RecordStartOptions struct {
	hasDuration      bool
	duration         time.Duration
	hasStreamProfile bool
	streamProfile    bilibili.StreamProfile
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

func WithStreamProfile(profile bilibili.StreamProfile) RecordStartOption {
	return func(o *RecordStartOptions) {
		o.hasStreamProfile = true
		o.streamProfile = profile
	}
}
