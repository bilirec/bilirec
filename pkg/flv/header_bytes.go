package flv

import "encoding/binary"

// NewFileHeaderBytes builds a complete FLV file preamble:
// 9-byte FLV header + 4-byte PreviousTagSize0.
func NewFileHeaderBytes() []byte {
	return NewFileHeaderBytesWithFlags(TypeFlagAudioVideo)
}

// NewFileHeaderBytesWithFlags builds an FLV preamble with the given TypeFlags byte.
func NewFileHeaderBytesWithFlags(flags byte) []byte {
	b := make([]byte, len(FlvHeader)+PrevTagSizeBytes)
	copy(b, FlvHeader)
	b[4] = flags
	return b
}

// FileHeaderTypeFlags returns the TypeFlags byte from an FLV file header, or 0 if too short.
func FileHeaderTypeFlags(hdr []byte) byte {
	if len(hdr) < 5 {
		return 0
	}
	return hdr[4]
}

// FileHeaderHasVideo reports whether hdr looks like an FLV preamble with the video flag set.
func FileHeaderHasVideo(hdr []byte) bool {
	return FileHeaderTypeFlags(hdr)&TypeFlagVideo != 0
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
