package ffmpeg

import "errors"

// ErrProbeUnavailable is returned when ffprobe is not on PATH (desktop) or
// not wired in this build (Android ffmpeg-kit).
var ErrProbeUnavailable = errors.New("ffprobe is not available")
