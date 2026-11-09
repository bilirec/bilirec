package subtitle

import (
	"testing"

	"github.com/bilirec/bilirec/pkg/flv"
)

const (
	testPMTPID   = 0x100
	testVideoPID = 0x101
)

func TestPmtVideoStream_pointerFieldZero(t *testing.T) {
	seg := tsSegment(
		tsPATPacket(testPMTPID, 0, false),
		tsPMTPacket(testPMTPID, testVideoPID, flv.VideoKindAVC, 0, false),
	)
	pid, codec := pmtVideoStream(seg)
	if pid != testVideoPID || codec != flv.VideoKindAVC {
		t.Fatalf("got pid=%#x codec=%v want %#x AVC", pid, codec, testVideoPID)
	}
}

func TestPmtVideoStream_pointerStuffing(t *testing.T) {
	seg := tsSegment(
		tsPATPacket(testPMTPID, 2, false),
		tsPMTPacket(testPMTPID, testVideoPID, flv.VideoKindAVC, 2, false),
	)
	pid, codec := pmtVideoStream(seg)
	if pid != testVideoPID || codec != flv.VideoKindAVC {
		t.Fatalf("got pid=%#x codec=%v want %#x AVC", pid, codec, testVideoPID)
	}
}

func TestPmtVideoStream_omittedPointerField(t *testing.T) {
	seg := tsSegment(
		tsPATPacket(testPMTPID, 0, true),
		tsPMTPacket(testPMTPID, testVideoPID, flv.VideoKindAVC, 0, true),
	)
	pid, codec := pmtVideoStream(seg)
	if pid != testVideoPID || codec != flv.VideoKindAVC {
		t.Fatalf("got pid=%#x codec=%v want %#x AVC", pid, codec, testVideoPID)
	}
}

func TestPmtVideoStream_hevcStreamType(t *testing.T) {
	const hevcPID = 0x102
	seg := tsSegment(
		tsPATPacket(testPMTPID, 0, false),
		tsPMTPacket(testPMTPID, hevcPID, flv.VideoKindHEVCEnhanced, 0, false),
	)
	pid, codec := pmtVideoStream(seg)
	if pid != hevcPID || codec != flv.VideoKindHEVCEnhanced {
		t.Fatalf("got pid=%#x codec=%v want %#x HEVC", pid, codec, hevcPID)
	}
}

func TestAccessUnitsFromTSChunk_subtitleSEI(t *testing.T) {
	seiPayload := mustSubtitleJSON(t, "ts-cue", 11)
	nals := annexBAVCSEINAL(seiPayload)
	pes := buildVideoPES(nals, 90_000) // 1000 ms

	seg := tsSegment(
		tsPATPacket(testPMTPID, 0, false),
		tsPMTPacket(testPMTPID, testVideoPID, flv.VideoKindAVC, 0, false),
		tsVideoPESPacket(testVideoPID, pes, true),
	)

	var sc tsScratch
	aus := accessUnitsFromTSChunk(seg, &sc)
	if au, ok := flushTSPES(&sc); ok {
		aus = append(aus, au)
	}
	if len(aus) != 1 {
		t.Fatalf("expected 1 access unit, got %d", len(aus))
	}
	cues := cuesFromSEI(aus[0])
	if len(cues) != 1 || cues[0].Text != "ts-cue" {
		t.Fatalf("unexpected cues %+v", cues)
	}
	if cues[0].PTSMs != 1000 {
		t.Fatalf("PTS ms=%d want 1000", cues[0].PTSMs)
	}
}

func TestAccessUnitsFromTSChunk_pesHeaderSplitAcrossPackets(t *testing.T) {
	seiPayload := mustSubtitleJSON(t, "split-header", 12)
	nals := annexBAVCSEINAL(seiPayload)
	pes := buildVideoPES(nals, 90_000) // 1000 ms；PES 頭 14 字節（含 PTS）

	// 第一包只放 8 字節負載，PES 頭因此跨到第二包。
	const firstPayload = 8
	first := tsPacketWithAdaptation(testVideoPID, true, pes[:firstPayload])
	packets := [][]byte{
		tsPATPacket(testPMTPID, 0, false),
		tsPMTPacket(testPMTPID, testVideoPID, flv.VideoKindAVC, 0, false),
		first,
	}
	for off := firstPayload; off < len(pes); off += tsPacketSize - 4 {
		end := min(off+tsPacketSize-4, len(pes))
		packets = append(packets, tsVideoPESPacket(testVideoPID, pes[off:end], false))
	}
	seg := tsSegment(packets...)

	var sc tsScratch
	aus := accessUnitsFromTSChunk(seg, &sc)
	if au, ok := flushTSPES(&sc); ok {
		aus = append(aus, au)
	}
	if len(aus) != 1 {
		t.Fatalf("expected 1 access unit, got %d", len(aus))
	}
	if !aus[0].ptsKnown {
		t.Fatal("PTS should be parsed after the header arrives in the next packet")
	}
	if aus[0].PTSMs != 1000 {
		t.Fatalf("PTS ms=%d want 1000", aus[0].PTSMs)
	}
	cues := cuesFromSEI(aus[0])
	if len(cues) != 1 || cues[0].Text != "split-header" {
		t.Fatalf("unexpected cues %+v", cues)
	}
}

// tsPacketWithAdaptation 构造带 adaptation field 的 TS 包，把 payload 貼到包尾，
// 中間用 0xFF 填充，讓 PES 頭可以跨包。
func tsPacketWithAdaptation(pid uint16, pusi bool, payload []byte) []byte {
	pkt := make([]byte, tsPacketSize)
	pkt[0] = 0x47
	pkt[1] = byte((pid >> 8) & 0x1F)
	pkt[2] = byte(pid & 0xFF)
	if pusi {
		pkt[1] |= 0x40
	}
	pkt[3] = 0x30 // adaptation field + payload
	// 4 字節 TS 頭 + 1 字節 adaptation_field_length + adaptLen + payload = 188
	adaptLen := tsPacketSize - 4 - 1 - len(payload)
	pkt[4] = byte(adaptLen)
	for i := 5; i < 5+adaptLen; i++ {
		pkt[i] = 0xFF
	}
	copy(pkt[5+adaptLen:], payload)
	return pkt
}

func tsSegment(packets ...[]byte) []byte {
	var seg []byte
	for _, p := range packets {
		seg = append(seg, p...)
	}
	return seg
}

func tsPATPacket(pmtPID uint16, pointer byte, omitPointer bool) []byte {
	section := []byte{
		0x00, 0xB0, 0x0D,
		0x00, 0x01,
		0xC1,
		0x00, 0x00,
		0x00, 0x01,
		byte(0xE0 | ((pmtPID >> 8) & 0x1F)), byte(pmtPID & 0xFF),
		0, 0, 0, 0,
	}
	payload := wrapPSIPayload(section, pointer, omitPointer)
	return tsPacket(0, true, payload)
}

func tsPMTPacket(pmtPID uint16, videoPID uint16, kind flv.VideoKind, pointer byte, omitPointer bool) []byte {
	streamType := byte(0x1B)
	if kind == flv.VideoKindHEVCEnhanced {
		streamType = 0x24
	}
	// 規範 PMT 佈局：table_id + section_length + program_number +
	// version/flags + section_number + last_section_number + PCR_PID +
	// program_info_length + 節目環（一個視訊 ES）+ CRC。
	sectionLen := 12 - 3 + 5 + 4 // program_number 之後的 header + 一個 ES 環 + CRC
	section := []byte{
		0x02, 0xB0 | byte((sectionLen>>8)&0x0F), byte(sectionLen),
		0x00, 0x01, // program_number
		0xC1, // version 0, current_next_indicator 1
		0x00, // section_number
		0x00, // last_section_number
		byte(0xE0 | ((videoPID >> 8) & 0x1F)), byte(videoPID & 0xFF), // PCR_PID
		0xF0, 0x00, // program_info_length = 0
		streamType,
		byte(0xE0 | ((videoPID >> 8) & 0x1F)), byte(videoPID & 0xFF),
		0xF0, 0x00, // ES_info_length = 0
		0, 0, 0, 0, // CRC32 placeholder
	}
	payload := wrapPSIPayload(section, pointer, omitPointer)
	return tsPacket(pmtPID, true, payload)
}

func wrapPSIPayload(section []byte, pointer byte, omitPointer bool) []byte {
	if omitPointer {
		return section
	}
	payload := make([]byte, 1+int(pointer)+len(section))
	payload[0] = pointer
	for i := 0; i < int(pointer); i++ {
		payload[1+i] = 0xFF
	}
	copy(payload[1+int(pointer):], section)
	return payload
}

func tsVideoPESPacket(videoPID uint16, pes []byte, pusi bool) []byte {
	return tsPacket(videoPID, pusi, pes)
}

func tsPacket(pid uint16, pusi bool, payload []byte) []byte {
	pkt := make([]byte, tsPacketSize)
	pkt[0] = 0x47
	pkt[1] = byte((pid >> 8) & 0x1F)
	pkt[2] = byte(pid & 0xFF)
	if pusi {
		pkt[1] |= 0x40
	}
	pkt[3] = 0x10
	if len(payload) > tsPacketSize-4 {
		panic("ts test payload too large")
	}
	copy(pkt[4:], payload)
	return pkt
}

func buildVideoPES(nals []byte, pts90k uint64) []byte {
	pes := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00, 0x80, 0x80, 0x05}
	pes = append(pes, encodePTS(pts90k)...)
	pes = append(pes, nals...)
	return pes
}

func encodePTS(pts90k uint64) []byte {
	return []byte{
		byte(0x21 | ((pts90k >> 29) & 0x0E)),
		byte((pts90k >> 22) & 0xFF),
		byte(0x01 | ((pts90k >> 14) & 0xFE)),
		byte((pts90k >> 7) & 0xFF),
		byte(0x01 | ((pts90k << 1) & 0xFE)),
	}
}

func annexBAVCSEINAL(payload []byte) []byte {
	rbsp := []byte{0x05}
	size := len(payload)
	for size >= 0xFF {
		rbsp = append(rbsp, 0xFF)
		size -= 0xFF
	}
	rbsp = append(rbsp, byte(size))
	rbsp = append(rbsp, payload...)
	rbsp = append(rbsp, 0x80)
	nal := append([]byte{0x06}, rbsp...)
	return append([]byte{0x00, 0x00, 0x00, 0x01}, nal...)
}
