package flv

// TagTimestamp reads the 32-bit FLV tag timestamp (milliseconds).
func TagTimestamp(tag []byte) uint32 {
	if len(tag) < 8 {
		return 0
	}
	return uint32(tag[4])<<16 | uint32(tag[5])<<8 | uint32(tag[6]) | uint32(tag[7])<<24
}

// SetTagTimestamp writes the 32-bit FLV tag timestamp (milliseconds).
func SetTagTimestamp(tag []byte, ts uint32) {
	if len(tag) < 8 {
		return
	}
	tag[4] = byte(ts >> 16)
	tag[5] = byte(ts >> 8)
	tag[6] = byte(ts)
	tag[7] = byte(ts >> 24)
}

// ZeroTagTimestamp returns a copy of tag with timestamp 0.
func ZeroTagTimestamp(tag []byte) []byte {
	if len(tag) == 0 {
		return nil
	}
	out := append([]byte(nil), tag...)
	SetTagTimestamp(out, 0)
	return out
}

// NextTag splits one complete FLV tag (header + data + PreviousTagSize) from buf.
// ok is false when buf is too short for a complete tag. skip is true when the
// leading bytes are not a valid tag; rest is then buf[1:] so the caller can resync.
func NextTag(buf []byte) (tag, rest []byte, ok, skip bool) {
	if len(buf) < TagHeaderSize {
		return nil, buf, false, false
	}
	tagType := buf[0]
	if tagType != TagTypeAudio && tagType != TagTypeVideo && tagType != TagTypeScript {
		return nil, buf[1:], false, true
	}
	dataSize := int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
	if dataSize <= 0 || dataSize > MaxTagDataSize {
		return nil, buf[1:], false, true
	}
	total := TagHeaderSize + dataSize + PrevTagSizeBytes
	if len(buf) < total {
		return nil, buf, false, false
	}
	return buf[:total], buf[total:], true, false
}

// TagData is the payload after the 11-byte tag header, excluding PreviousTagSize.
func TagData(tag []byte) []byte {
	if len(tag) < TagHeaderSize+PrevTagSizeBytes {
		return nil
	}
	return tag[TagHeaderSize : len(tag)-PrevTagSizeBytes]
}
