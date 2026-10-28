package hls

import "time"

const defaultSegmentDuration = 2 * time.Second

// PlaylistSequenceGapDuration estimates missing media time from a playlist sequence hole.
func PlaylistSequenceGapDuration(pl *Playlist, lost int64) time.Duration {
	if lost <= 0 || pl == nil {
		return 0
	}
	segDur := pl.TargetDuration
	if segDur <= 0 && len(pl.Segments) > 0 {
		segDur = pl.Segments[0].Duration
	}
	if segDur <= 0 {
		return time.Duration(lost) * defaultSegmentDuration
	}
	return time.Duration(float64(lost) * segDur * float64(time.Second))
}
