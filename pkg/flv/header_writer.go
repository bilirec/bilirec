package flv

// FlvHeaderWriter prepends a FLV file preamble and optional sequence-header
// tags to a data chunk. It is stateless; the caller (typically a processor)
// is responsible for ensuring Prepend is called only once per file segment.
type FlvHeaderWriter struct {
	// VideoHeaderTag is an optional complete FLV tag bytes
	// (TagHeader + Data + PrevTagSize) for the video sequence header.
	VideoHeaderTag []byte
	// AudioHeaderTag is an optional complete FLV tag bytes
	// (TagHeader + Data + PrevTagSize) for the audio sequence header.
	AudioHeaderTag []byte
}

func normalizeTagTimestamp(tag []byte) []byte {
	if len(tag) == 0 {
		return nil
	}
	out := append([]byte(nil), tag...)
	if len(out) >= TagHeaderSize {
		// Always start injected sequence-header tags from timestamp 0 in a new segment.
		out[4] = 0
		out[5] = 0
		out[6] = 0
		out[7] = 0
	}
	return out
}

// Prepend merges the FLV file header, optional video/audio sequence-header
// tags, and data into one byte slice.
func (w *FlvHeaderWriter) Prepend(data []byte) []byte {
	fileHeader := NewFileHeaderBytes() // 9-byte FLV magic + 4-byte PrevTagSize0
	vTag := normalizeTagTimestamp(w.VideoHeaderTag)
	aTag := normalizeTagTimestamp(w.AudioHeaderTag)
	size := len(fileHeader) + len(vTag) + len(aTag) + len(data)
	result := make([]byte, 0, size)
	result = append(result, fileHeader...)
	if len(vTag) > 0 {
		result = append(result, vTag...)
	}
	if len(aTag) > 0 {
		result = append(result, aTag...)
	}
	result = append(result, data...)
	return result
}
