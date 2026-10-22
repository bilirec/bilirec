package mp4

import (
	"encoding/binary"
	"io"
	"os"
	"strings"
)

const (
	boxHeaderLen    = 8
	boxHeaderLen64  = 16
	boxSizeUse64    = 1
	boxSizeUntilEOF = 0
	boxTypeMoov     = 0x6d6f6f76 // "moov"
)

// IsISOBMFF reports whether format is an ISO BMFF family that stores a moov box.
func IsISOBMFF(format string) bool {
	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "mp4", "m4v", "mov":
		return true
	default:
		return false
	}
}

// HasMoov reports whether path contains a top-level moov box.
// Truncated or malformed files return false with a nil error.
func HasMoov(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	return HasTopLevelBox(f, info.Size(), boxTypeMoov)
}

// HasTopLevelBox walks top-level ISO BMFF boxes in r and reports whether boxType is present.
// It seeks by the size field and does not read box payloads. A truncated or invalid
// header ends the walk without an error (false, nil). I/O errors other than EOF are returned.
func HasTopLevelBox(r io.ReaderAt, size int64, boxType uint32) (bool, error) {
	if size < boxHeaderLen {
		return false, nil
	}

	var offset int64
	for offset < size {
		boxSize, typ, err := readBoxHeader(r, offset, size)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return false, nil
			}
			return false, err
		}
		if boxSize == 0 {
			return false, nil
		}
		if typ == boxType {
			return true, nil
		}
		next := offset + boxSize
		if next <= offset {
			return false, nil
		}
		offset = next
	}
	return false, nil
}

func readBoxHeader(r io.ReaderAt, offset, fileSize int64) (boxSize int64, typ uint32, err error) {
	if offset < 0 || offset+boxHeaderLen > fileSize {
		return 0, 0, io.ErrUnexpectedEOF
	}

	var hdr [boxHeaderLen64]byte
	if _, err := r.ReadAt(hdr[:boxHeaderLen], offset); err != nil {
		return 0, 0, err
	}

	size32 := binary.BigEndian.Uint32(hdr[0:4])
	typ = binary.BigEndian.Uint32(hdr[4:8])
	headerLen := int64(boxHeaderLen)
	var size uint64

	switch size32 {
	case boxSizeUntilEOF:
		size = uint64(fileSize - offset)
	case boxSizeUse64:
		if offset+boxHeaderLen64 > fileSize {
			return 0, 0, io.ErrUnexpectedEOF
		}
		if _, err := r.ReadAt(hdr[boxHeaderLen:boxHeaderLen64], offset+boxHeaderLen); err != nil {
			return 0, 0, err
		}
		size = binary.BigEndian.Uint64(hdr[boxHeaderLen:boxHeaderLen64])
		headerLen = boxHeaderLen64
	default:
		size = uint64(size32)
	}

	if size < uint64(headerLen) {
		return 0, 0, nil
	}
	remain := uint64(fileSize - offset)
	if size > remain {
		return 0, 0, nil
	}
	return int64(size), typ, nil
}
