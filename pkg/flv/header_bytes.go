package flv

import "encoding/binary"

// NewFileHeaderBytes builds a complete FLV file preamble:
// 9-byte FLV header + 4-byte PreviousTagSize0.
func NewFileHeaderBytes() []byte {
	b := make([]byte, len(FlvHeader)+PrevTagSizeBytes)
	copy(b, FlvHeader)
	// PreviousTagSize0 is 0 by FLV spec. The last 4 bytes are already zeroed.
	return b
}

func NewTagBytes(tagType byte, data []byte) []byte {
	b := make([]byte, TagHeaderSize+len(data)+PrevTagSizeBytes)
	b[0] = tagType
	b[1] = byte(len(data) >> 16)
	b[2] = byte(len(data) >> 8)
	b[3] = byte(len(data))
	copy(b[TagHeaderSize:], data)
	binary.BigEndian.PutUint32(b[TagHeaderSize+len(data):], uint32(TagHeaderSize+len(data)))
	return b
}
