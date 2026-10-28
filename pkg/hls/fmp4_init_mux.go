package hls

import (
	"bytes"
	"encoding/binary"
	"sort"
)

const (
	boxTypeMvex = 0x6d766578 // "mvex"
	boxTypeTrex = 0x74726578 // "trex"
	boxTypeTkhd = 0x746b6864 // "tkhd"
	boxTypeHdlr = 0x68646c72 // "hdlr"
	boxTypeMinf = 0x6d696e66 // "minf"
	boxTypeStbl = 0x7374626c // "stbl"
	boxTypeStsd = 0x73747364 // "stsd"
)

type muxTrackSpec struct {
	trackID   uint32
	handler   [4]byte
	timescale uint32
	width     uint32
	height    uint32
	sample    []byte
}

type muxTrexSpec struct {
	trackID  uint32
	duration uint32
	size     uint32
	flags    uint32
}

type initMuxIdentity struct {
	tracks []muxTrackSpec
	trex   []muxTrexSpec
}

// InitMuxCompatible reports whether two fMP4 init segments describe the same
// mux/codec parameters. Metadata-only differences (mvhd duration, etc.) are ignored.
// Unparseable inits or ftyp-only payloads are never compatible unless bytes are equal.
func InitMuxCompatible(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	ia, okA := parseInitMuxIdentity(a)
	ib, okB := parseInitMuxIdentity(b)
	if !okA || !okB {
		return false
	}
	return ia.equal(ib)
}

func (id initMuxIdentity) equal(other initMuxIdentity) bool {
	if len(id.tracks) != len(other.tracks) || len(id.trex) != len(other.trex) {
		return false
	}
	for i := range id.tracks {
		t, o := id.tracks[i], other.tracks[i]
		if t.trackID != o.trackID || t.handler != o.handler || t.timescale != o.timescale ||
			t.width != o.width || t.height != o.height || !bytes.Equal(t.sample, o.sample) {
			return false
		}
	}
	for i := range id.trex {
		t, o := id.trex[i], other.trex[i]
		if t.trackID != o.trackID || t.duration != o.duration || t.size != o.size || t.flags != o.flags {
			return false
		}
	}
	return true
}

func parseInitMuxIdentity(init []byte) (initMuxIdentity, bool) {
	var out initMuxIdentity
	var hasMedia bool
	for off := 0; off < len(init); {
		size, typ, _, ok := readBoxHeaderCode(init, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeMoov {
			parseMoovMux(init, off+8, boxEnd, &out, &hasMedia)
		}
		off = boxEnd
	}
	if !hasMedia || len(out.tracks) == 0 {
		return initMuxIdentity{}, false
	}
	sort.Slice(out.tracks, func(i, j int) bool { return out.tracks[i].trackID < out.tracks[j].trackID })
	sort.Slice(out.trex, func(i, j int) bool { return out.trex[i].trackID < out.trex[j].trackID })
	return out, true
}

func parseMoovMux(data []byte, start, end int, out *initMuxIdentity, hasMedia *bool) {
	for off := start; off < end; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		switch typ {
		case boxTypeTrak:
			if tr, ok := parseTrakMux(data, off+8, boxEnd); ok {
				out.tracks = append(out.tracks, tr)
				if tr.handler[0] == 'v' || tr.handler[0] == 's' {
					*hasMedia = true
				}
			}
		case boxTypeMvex:
			parseMvexMux(data, off+8, boxEnd, out)
		}
		off = boxEnd
	}
}

func parseMvexMux(data []byte, start, end int, out *initMuxIdentity) {
	for off := start; off < end; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeTrex {
			if tx, ok := parseTrexMux(data, off, boxEnd); ok {
				out.trex = append(out.trex, tx)
			}
		}
		off = boxEnd
	}
}

func parseTrexMux(data []byte, start, end int) (muxTrexSpec, bool) {
	if end-start < 32 {
		return muxTrexSpec{}, false
	}
	trackID := binary.BigEndian.Uint32(data[start+12 : start+16])
	if trackID == 0 {
		return muxTrexSpec{}, false
	}
	return muxTrexSpec{
		trackID:  trackID,
		duration: binary.BigEndian.Uint32(data[start+20 : start+24]),
		size:     binary.BigEndian.Uint32(data[start+24 : start+28]),
		flags:    binary.BigEndian.Uint32(data[start+28 : start+32]),
	}, true
}

func parseTrakMux(data []byte, start, end int) (muxTrackSpec, bool) {
	var tr muxTrackSpec
	for off := start; off < end; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		switch typ {
		case boxTypeTkhd:
			id, w, h, okID := tkhdMuxFields(data, off, boxEnd)
			if okID {
				tr.trackID = id
				tr.width, tr.height = w, h
			}
		case boxTypeMdia:
			handler, ts, sample, okMdia := mdiaMuxFields(data, off+8, boxEnd)
			if okMdia {
				tr.handler = handler
				if ts > 0 {
					tr.timescale = ts
				}
				if len(sample) > 0 {
					tr.sample = sample
				}
			}
		}
		off = boxEnd
	}
	if tr.trackID == 0 || tr.timescale == 0 {
		return muxTrackSpec{}, false
	}
	if tr.handler != handlerType("vide") && tr.handler != handlerType("soun") {
		return muxTrackSpec{}, false
	}
	return tr, true
}

func handlerType(s string) [4]byte {
	var h [4]byte
	copy(h[:], s)
	return h
}

func tkhdMuxFields(data []byte, start, end int) (trackID, width, height uint32, ok bool) {
	if end-start < 20 {
		return 0, 0, 0, false
	}
	if data[start+8] == 1 {
		if end-start < 84 {
			return 0, 0, 0, false
		}
		trackID = binary.BigEndian.Uint32(data[start+20 : start+24])
		width = binary.BigEndian.Uint32(data[start+76 : start+80])
		height = binary.BigEndian.Uint32(data[start+80 : start+84])
		return trackID, width, height, trackID != 0
	}
	if end-start < 84 {
		return 0, 0, 0, false
	}
	trackID = binary.BigEndian.Uint32(data[start+12 : start+16])
	width = binary.BigEndian.Uint32(data[start+76 : start+80])
	height = binary.BigEndian.Uint32(data[start+80 : start+84])
	return trackID, width, height, trackID != 0
}

func mdiaMuxFields(data []byte, start, end int) (handler [4]byte, timescale uint32, sample []byte, ok bool) {
	for off := start; off < end; {
		size, typ, _, okBox := readBoxHeaderCode(data, off)
		if !okBox {
			break
		}
		boxEnd := off + size
		switch typ {
		case boxTypeMdhd:
			if ts, okTS := mdhdTimescale(data, off, boxEnd); okTS {
				timescale = ts
			}
		case boxTypeHdlr:
			if boxEnd-off >= 20 {
				copy(handler[:], data[off+16:off+20])
			}
		case boxTypeMinf:
			if ent := firstStsdSampleEntry(data, off+8, boxEnd); len(ent) > 0 {
				sample = ent
			}
		}
		off = boxEnd
	}
	ok = timescale > 0 && (handler == handlerType("vide") || handler == handlerType("soun"))
	return handler, timescale, sample, ok
}

func firstStsdSampleEntry(data []byte, start, end int) []byte {
	for off := start; off < end; {
		size, typ, _, ok := readBoxHeaderCode(data, off)
		if !ok {
			break
		}
		boxEnd := off + size
		if typ == boxTypeStsd {
			if boxEnd-off > 16 {
				return append([]byte(nil), data[off+16:boxEnd]...)
			}
			return nil
		}
		if typ == boxTypeStbl {
			if ent := firstStsdSampleEntry(data, off+8, boxEnd); len(ent) > 0 {
				return ent
			}
		}
		off = boxEnd
	}
	return nil
}
