package hls

import (
	"encoding/binary"
	"time"
)

const (
	boxTypeMoov = 0x6d6f6f76 // "moov"
	boxTypeTrak = 0x7472616b // "trak"
	boxTypeMdia = 0x6d646961 // "mdia"
	boxTypeMdhd = 0x6d646864 // "mdhd"
)

const (
	fmp4TimelineBackwardThreshold = 500 * time.Millisecond
	fmp4TimelineForwardThreshold  = 10 * time.Second
)

// SegmentHasMoov reports whether data contains a top-level moov box (init segment).
func SegmentHasMoov(data []byte) bool {
	for off := 0; off < len(data); {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		if typ == boxTypeMoov {
			return true
		}
		off += size
	}
	return false
}

// TrackTimescalesFromInit maps each trak track_ID to that trak's mdhd timescale.
// A missing or truncated trak is omitted. The result is nil when no trak yields both fields.
func TrackTimescalesFromInit(init []byte) map[uint32]uint32 {
	if !SegmentHasMoov(init) {
		return nil
	}
	out := make(map[uint32]uint32)
	for off := 0; off < len(init); {
		size, typ, _, ok := readBoxHeaderCode(init, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeMoov {
			collectTrackTimescales(init, off+8, boxEnd, out)
		}
		off = boxEnd
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func collectTrackTimescales(data []byte, start, end int, out map[uint32]uint32) {
	for off := start; off < end; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeTrak {
			if id, ts, okTrak := trakIDAndTimescale(data, off+8, boxEnd); okTrak {
				out[id] = ts
			}
		}
		off = boxEnd
	}
}

func trakIDAndTimescale(data []byte, start, end int) (trackID, timescale uint32, ok bool) {
	var haveID, haveTS bool
	for off := start; off < end; {
		size, typ, _, okBox := readBoxHeaderCode(data, off)
		if !okBox {
			break
		}
		boxEnd := off + size
		switch typ {
		case boxTypeTkhd:
			id, _, _, okID := tkhdMuxFields(data, off, boxEnd)
			if okID {
				trackID = id
				haveID = true
			}
		case boxTypeMdia:
			if ts, okTS := mdhdTimescaleIn(data, off+8, boxEnd); okTS {
				timescale = ts
				haveTS = true
			}
		}
		off = boxEnd
	}
	if !haveID || !haveTS || timescale == 0 {
		return 0, 0, false
	}
	return trackID, timescale, true
}

func mdhdTimescaleIn(data []byte, start, end int) (uint32, bool) {
	for off := start; off < end; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeMdhd {
			if ts, okTS := mdhdTimescale(data, off, boxEnd); okTS {
				return ts, true
			}
		}
		off = boxEnd
	}
	return 0, false
}

func mdhdTimescale(data []byte, start, end int) (uint32, bool) {
	if end-start < 20 {
		return 0, false
	}
	if data[start+8] == 1 {
		if end-start < 32 {
			return 0, false
		}
		return binary.BigEndian.Uint32(data[start+28 : start+32]), true
	}
	return binary.BigEndian.Uint32(data[start+20 : start+24]), true
}

// NoteFragmentTimeline compares raw tfdt decode times against last per track.
// timescales is track_ID → mdhd timescale. A track missing from the map is skipped.
// It updates last in place and returns the largest positive gap duration to report.
func NoteFragmentTimeline(data []byte, timescales map[uint32]uint32, last map[uint32]uint64) time.Duration {
	if len(timescales) == 0 {
		return 0
	}

	var maxGap time.Duration
	for off := 0; off < len(data); {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeMoof {
			gap := noteMoofTimeline(data, off+8, boxEnd, timescales, last)
			if gap > maxGap {
				maxGap = gap
			}
		}
		off = boxEnd
	}
	return maxGap
}

func noteMoofTimeline(data []byte, start, end int, timescales map[uint32]uint32, last map[uint32]uint64) time.Duration {
	var maxGap time.Duration
	for off := start; off < end; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeTraf {
			gap := noteTrafTimeline(data, off, boxEnd, timescales, last)
			if gap > maxGap {
				maxGap = gap
			}
		}
		off = boxEnd
	}
	return maxGap
}

func noteTrafTimeline(data []byte, trafStart, trafEnd int, timescales map[uint32]uint32, last map[uint32]uint64) time.Duration {
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
	timescale := timescales[trackID]
	if trackID == 0 || timescale == 0 {
		return 0
	}

	for off := trafStart + 8; off < trafEnd; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeTfdt {
			raw, ok := rawTfdtValue(data, off, boxEnd)
			if !ok {
				return 0
			}
			prev, seen := last[trackID]
			last[trackID] = raw
			if !seen {
				return 0
			}
			return timelineGapDuration(prev, raw, timescale)
		}
		off = boxEnd
	}
	return 0
}

func rawTfdtValue(data []byte, start, end int) (uint64, bool) {
	if end-start < 16 {
		return 0, false
	}
	version := data[start+8]
	if version == 1 {
		if end-start < 20 {
			return 0, false
		}
		return binary.BigEndian.Uint64(data[start+12 : start+20]), true
	}
	return uint64(binary.BigEndian.Uint32(data[start+12 : start+16])), true
}

func timelineGapDuration(prev, cur uint64, timescale uint32) time.Duration {
	if timescale == 0 {
		return 0
	}
	tick := time.Second / time.Duration(timescale)
	if cur < prev {
		back := time.Duration(prev-cur) * tick
		if back > fmp4TimelineBackwardThreshold {
			return back
		}
		return 0
	}
	if cur > prev {
		fwd := time.Duration(cur-prev) * tick
		if fwd > fmp4TimelineForwardThreshold {
			return fwd
		}
	}
	return 0
}
