package hls

import "encoding/binary"

const (
	boxTypeMoof = 0x6d6f6f66 // "moof"
	boxTypeTraf = 0x74726166 // "traf"
	boxTypeTfhd = 0x74666864 // "tfhd"
	boxTypeTfdt = 0x74666474 // "tfdt"
)

func readBoxHeaderCode(data []byte, offset int) (size int, boxType uint32, headerLen int, ok bool) {
	if offset < 0 || offset+8 > len(data) {
		return 0, 0, 0, false
	}
	size32 := binary.BigEndian.Uint32(data[offset : offset+4])
	boxType = binary.BigEndian.Uint32(data[offset+4 : offset+8])
	headerLen = 8

	switch size32 {
	case 0:
		size = len(data) - offset
	case 1:
		if offset+16 > len(data) {
			return 0, 0, 0, false
		}
		size64 := binary.BigEndian.Uint64(data[offset+8 : offset+16])
		if size64 > uint64(len(data)-offset) {
			return 0, 0, 0, false
		}
		headerLen = 16
		size = int(size64)
	default:
		size = int(size32)
	}

	if size < headerLen || offset+size > len(data) {
		return 0, 0, 0, false
	}
	return size, boxType, headerLen, true
}

func ReadBoxHeader(data []byte, offset int) (size int, boxType string, headerLen int, ok bool) {
	size, boxTypeCode, headerLen, ok := readBoxHeaderCode(data, offset)
	if !ok {
		return 0, "", 0, false
	}
	b := [4]byte{}
	binary.BigEndian.PutUint32(b[:], boxTypeCode)
	boxType = string(b[:])
	return size, boxType, headerLen, true
}

func TfhdTrackID(data []byte, start, end int) (uint32, bool) {
	if end-start < 16 {
		return 0, false
	}
	// FullBox(4 bytes version+flags) + track_ID(4 bytes)
	trackID := binary.BigEndian.Uint32(data[start+12 : start+16])
	if trackID == 0 {
		return 0, false
	}
	return trackID, true
}

func NormalizeTfdtBox(data []byte, start, end int, trackID uint32, bases map[uint32]uint64) bool {
	if trackID == 0 || end-start < 16 {
		return false
	}
	version := data[start+8]

	if version == 1 {
		if end-start < 20 {
			return false
		}
		value := binary.BigEndian.Uint64(data[start+12 : start+20])
		base, exists := bases[trackID]
		if !exists || value < base {
			bases[trackID] = value
			base = value
		}
		binary.BigEndian.PutUint64(data[start+12:start+20], value-base)
		return true
	}

	value := uint64(binary.BigEndian.Uint32(data[start+12 : start+16]))
	base, exists := bases[trackID]
	if !exists || value < base {
		bases[trackID] = value
		base = value
	}
	binary.BigEndian.PutUint32(data[start+12:start+16], uint32(value-base))
	return true
}

func NormalizeTrafTfdt(data []byte, trafStart, trafEnd int, bases map[uint32]uint64) int {
	trackID := uint32(0)

	for off := trafStart + 8; off < trafEnd; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeTfhd {
			if id, found := TfhdTrackID(data, off, boxEnd); found {
				trackID = id
				break
			}
		}
		off = boxEnd
	}

	if trackID == 0 {
		return 0
	}

	normalized := 0
	for off := trafStart + 8; off < trafEnd; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeTfdt && NormalizeTfdtBox(data, off, boxEnd, trackID, bases) {
			normalized++
		}
		off = boxEnd
	}

	return normalized
}

func NormalizeFragmentTimestamps(data []byte, bases map[uint32]uint64) int {
	total := 0
	for off := 0; off < len(data); {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size

		if typ == boxTypeMoof {
			for child := off + 8; child < boxEnd; {
				childSize, childType, _, childOK := readBoxHeaderCode(data, child)
				if !childOK {
					break
				}
				childEnd := child + childSize
				if childType == boxTypeTraf {
					total += NormalizeTrafTfdt(data, child, childEnd, bases)
				}
				child = childEnd
			}
		}

		off = boxEnd
	}
	return total
}
