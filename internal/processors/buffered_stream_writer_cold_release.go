package processors

const (
	coldReleaseHotWindowMin = 64 * 1024 * 1024
	coldReleaseHotWindowMul  = 4
	coldReleaseMinRelease    = 32 * 1024 * 1024
)

func coldReleaseHotWindow(bufferSize int) int64 {
	hot := int64(coldReleaseHotWindowMul) * int64(bufferSize)
	if hot < coldReleaseHotWindowMin {
		return coldReleaseHotWindowMin
	}
	return hot
}

// coldReleasePlan describes a page-cache release for the cold file prefix.
type coldReleasePlan struct {
	Offset int64
	Length int64
	NewEnd int64
}

// planColdRelease returns whether a cold release should run and the byte range to release.
func planColdRelease(onDisk, releasedThrough int64, bufferSize int) (coldReleasePlan, bool) {
	if onDisk <= 0 {
		return coldReleasePlan{}, false
	}
	hotWindow := coldReleaseHotWindow(bufferSize)
	coldEnd := onDisk - hotWindow
	if coldEnd <= releasedThrough {
		return coldReleasePlan{}, false
	}
	length := coldEnd - releasedThrough
	if length < coldReleaseMinRelease {
		return coldReleasePlan{}, false
	}
	return coldReleasePlan{
		Offset: releasedThrough,
		Length: length,
		NewEnd: coldEnd,
	}, true
}
