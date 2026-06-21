package filecache

const pageSize = 4096

func alignDown(offset int64) int64 {
	if offset <= 0 {
		return 0
	}
	return offset &^ (pageSize - 1)
}

// alignedColdRange returns page-aligned [off, off+length) for a cold release window.
// Returns ok=false when the aligned range is empty.
func alignedColdRange(offset, length int64) (off, alignedLen int64, ok bool) {
	if length <= 0 {
		return 0, 0, false
	}
	off = alignDown(offset)
	end := offset + length
	alignedEnd := alignDown(end)
	if alignedEnd <= off {
		return 0, 0, false
	}
	return off, alignedEnd - off, true
}
