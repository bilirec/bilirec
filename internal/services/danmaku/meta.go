package danmaku

import (
	"strconv"
	"time"
)

// RoomMeta carries the room information written into the danmaku file header.
type RoomMeta struct {
	RoomID  int64
	ShortID int64
	Uname   string
	Title   string
}

// formatRelativeTS renders seconds since segmentStart with millisecond
// precision (F3), clamped to zero for early messages.
func formatRelativeTS(segmentStart, now time.Time) string {
	secs := now.Sub(segmentStart).Seconds()
	if secs < 0 {
		secs = 0
	}
	return strconv.FormatFloat(secs, 'f', 3, 64)
}
