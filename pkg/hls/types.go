package hls

// Segment represents a single media segment in a playlist.
type Segment struct {
	URI      string
	Duration float64
}

// Playlist is a compact media-playlist model consumed by stream pipelines.
type Playlist struct {
	MediaSeq       int64
	TargetDuration float64
	Segments       []Segment
	MapURI         string
}

// SegmentFetchResult contains asynchronous fetch result.
type SegmentFetchResult struct {
	Data []byte
	Err  error
}
