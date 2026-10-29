package hls

import (
	"encoding/binary"
	"testing"
)

func TestInitMuxCompatible_ByteEqual(t *testing.T) {
	a := []byte{1, 2, 3}
	if !InitMuxCompatible(a, append([]byte(nil), a...)) {
		t.Fatal("identical bytes should be compatible")
	}
}

func TestInitMuxCompatible_FtypOnlyNotCompatible(t *testing.T) {
	a := makeFtypInitTest("isom")
	b := makeFtypInitTest("mp42")
	if InitMuxCompatible(a, b) {
		t.Fatal("ftyp-only inits must not be mux-compatible")
	}
}

func TestInitMuxCompatible_MvhdDurationOnly(t *testing.T) {
	base := buildTestInit(1000, 1280<<16, []byte{0x01, 0x02}, 0)
	other := buildTestInit(9999, 1280<<16, []byte{0x01, 0x02}, 0)
	if !InitMuxCompatible(base, other) {
		t.Fatal("mvhd duration-only change should stay compatible")
	}
}

func TestInitMuxCompatible_WidthChange(t *testing.T) {
	a := buildTestInit(1000, 1280<<16, []byte{0x01}, 0)
	b := buildTestInit(1000, 1920<<16, []byte{0x01}, 0)
	if InitMuxCompatible(a, b) {
		t.Fatal("width change must not be compatible")
	}
}

func TestInitMuxCompatible_AvcCChange(t *testing.T) {
	a := buildTestInit(1000, 1280<<16, []byte{0x01, 0x02}, 0)
	b := buildTestInit(1000, 1280<<16, []byte{0x01, 0x03}, 0)
	if InitMuxCompatible(a, b) {
		t.Fatal("decoder config change must not be compatible")
	}
}

func makeFtypInitTest(brand string) []byte {
	init := make([]byte, 20)
	binary.BigEndian.PutUint32(init[0:4], 20)
	copy(init[4:8], "ftyp")
	copy(init[8:12], []byte(brand))
	copy(init[16:20], []byte(brand))
	return init
}

// buildTestInit builds ftyp+moov with one video trak and optional trex in mvex.
func buildTestInit(mvhdDuration uint32, tkhdWidth uint32, avcC []byte, trexDur uint32) []byte {
	ftyp := makeFtypInitTest("isom")
	moov := buildMoovTest(mvhdDuration, tkhdWidth, avcC, trexDur)
	return append(ftyp, moov...)
}

func buildMoovTest(mvhdDuration, tkhdWidth uint32, avcC []byte, trexDur uint32) []byte {
	trak := buildTrakTest(tkhdWidth, avcC)
	mvex := []byte{}
	if trexDur > 0 {
		mvex = buildMvexTrex(1, trexDur, 0, 0)
	}
	mvhd := buildMvhdBox(mvhdDuration)
	children := append(mvhd, trak...)
	children = append(children, mvex...)
	moov := make([]byte, 8+len(children))
	binary.BigEndian.PutUint32(moov[0:4], uint32(len(moov)))
	copy(moov[4:8], "moov")
	copy(moov[8:], children)
	return moov
}

func buildMvhdBox(duration uint32) []byte {
	// version 0 mvhd: 100 bytes typical
	const mvhdLen = 108
	b := make([]byte, mvhdLen)
	binary.BigEndian.PutUint32(b[0:4], mvhdLen)
	copy(b[4:8], "mvhd")
	b[8] = 0
	binary.BigEndian.PutUint32(b[20:24], duration)
	binary.BigEndian.PutUint32(b[24:28], 1000)
	return b
}

func buildTrakTest(width uint32, avcC []byte) []byte {
	tkhd := buildTkhdBox(1, width, 720<<16)
	mdia := buildMdiaVideo(avcC)
	children := append(tkhd, mdia...)
	trak := make([]byte, 8+len(children))
	binary.BigEndian.PutUint32(trak[0:4], uint32(len(trak)))
	copy(trak[4:8], "trak")
	copy(trak[8:], children)
	return trak
}

func buildTkhdBox(trackID, width, height uint32) []byte {
	const tkhdLen = 92
	b := make([]byte, tkhdLen)
	binary.BigEndian.PutUint32(b[0:4], tkhdLen)
	copy(b[4:8], "tkhd")
	b[8] = 0
	binary.BigEndian.PutUint32(b[12:16], 0x01020304) // creation_time; must not be read as track_ID
	binary.BigEndian.PutUint32(b[20:24], trackID)
	binary.BigEndian.PutUint32(b[84:88], width)
	binary.BigEndian.PutUint32(b[88:92], height)
	return b
}

func TestTkhdMuxFieldsV0(t *testing.T) {
	b := buildTkhdBox(7, 320<<16, 240<<16)
	id, w, h, ok := tkhdMuxFields(b, 0, len(b))
	if !ok || id != 7 || w != 320<<16 || h != 240<<16 {
		t.Fatalf("v0 tkhd: id=%d width=%#x height=%#x ok=%v", id, w, h, ok)
	}
}

func TestTkhdMuxFieldsV1(t *testing.T) {
	const n = 104
	b := make([]byte, n)
	binary.BigEndian.PutUint32(b[0:4], n)
	copy(b[4:8], "tkhd")
	b[8] = 1
	binary.BigEndian.PutUint32(b[12:16], 0x11111111)
	binary.BigEndian.PutUint32(b[20:24], 0x22222222)
	binary.BigEndian.PutUint32(b[28:32], 3)
	binary.BigEndian.PutUint32(b[96:100], 320<<16)
	binary.BigEndian.PutUint32(b[100:104], 240<<16)
	id, w, h, ok := tkhdMuxFields(b, 0, len(b))
	if !ok || id != 3 || w != 320<<16 || h != 240<<16 {
		t.Fatalf("v1 tkhd: id=%d width=%#x height=%#x ok=%v", id, w, h, ok)
	}
}

func buildMdiaVideo(avcC []byte) []byte {
	mdhd := buildMdhdBox(90000)
	hdlr := buildHdlrBox("vide")
	minf := buildMinfStsd(avcC)
	children := append(mdhd, hdlr...)
	children = append(children, minf...)
	mdia := make([]byte, 8+len(children))
	binary.BigEndian.PutUint32(mdia[0:4], uint32(len(mdia)))
	copy(mdia[4:8], "mdia")
	copy(mdia[8:], children)
	return mdia
}

func buildMdhdBox(timescale uint32) []byte {
	const mdhdLen = 32
	b := make([]byte, mdhdLen)
	binary.BigEndian.PutUint32(b[0:4], mdhdLen)
	copy(b[4:8], "mdhd")
	b[8] = 0
	binary.BigEndian.PutUint32(b[20:24], timescale)
	return b
}

func buildHdlrBox(handler string) []byte {
	const hdlrLen = 44
	b := make([]byte, hdlrLen)
	binary.BigEndian.PutUint32(b[0:4], hdlrLen)
	copy(b[4:8], "hdlr")
	copy(b[16:20], []byte(handler))
	return b
}

func buildMinfStsd(avcC []byte) []byte {
	avc1 := buildAvc1SampleEntry(avcC)
	stsd := buildStsdBox(avc1)
	stbl := wrapBox("stbl", stsd)
	minf := wrapBox("minf", stbl)
	return minf
}

func buildAvc1SampleEntry(avcC []byte) []byte {
	const avc1Header = 86
	avcCBox := wrapBox("avcC", avcC)
	body := make([]byte, avc1Header+len(avcCBox))
	binary.BigEndian.PutUint32(body[0:4], uint32(len(body)))
	copy(body[4:8], "avc1")
	copy(body[avc1Header:], avcCBox)
	return body
}

func buildStsdBox(sampleEntry []byte) []byte {
	payload := make([]byte, 8+len(sampleEntry))
	binary.BigEndian.PutUint32(payload[4:8], 1)
	copy(payload[8:], sampleEntry)
	return wrapBox("stsd", payload)
}

func buildMvexTrex(trackID, dur, size, flags uint32) []byte {
	trex := make([]byte, 32)
	binary.BigEndian.PutUint32(trex[0:4], 32)
	copy(trex[4:8], "trex")
	binary.BigEndian.PutUint32(trex[12:16], trackID)
	binary.BigEndian.PutUint32(trex[20:24], dur)
	binary.BigEndian.PutUint32(trex[24:28], size)
	binary.BigEndian.PutUint32(trex[28:32], flags)
	return wrapBox("mvex", trex)
}

func wrapBox(typ string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	copy(out[4:8], []byte(typ))
	copy(out[8:], payload)
	return out
}
