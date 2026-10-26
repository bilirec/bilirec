package subtitle

import (
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/sei"
)

func TestCuesFromEmbeddedUUID(t *testing.T) {
	items := []rawCue{{Sub: "embedded", SubID: 9, OTMs: 0, DTMs: 500}}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	blob := append(append([]byte("noise"), subtitleUUID...), payload...)
	cues := cuesFromEmbeddedUUID(blob, 1000)
	if len(cues) != 1 || cues[0].Text != "embedded" {
		t.Fatalf("unexpected cues %+v", cues)
	}
}

func TestCuesFromSEI_BilibiliUUID(t *testing.T) {
	items := []rawCue{{Sub: "hello", SubID: 1, OTMs: 0, DTMs: 2000}}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	seiPayload := append(append([]byte{}, subtitleUUID...), payload...)
	nal := buildAVCSEINAL(seiPayload)
	au := accessUnit{PTSMs: 1000, Codec: flv.VideoKindAVC, NALs: nal}
	cues := cuesFromSEI(au)
	if len(cues) != 1 {
		t.Fatalf("expected 1 cue, got %d", len(cues))
	}
	if cues[0].Text != "hello" || cues[0].ID != 1 {
		t.Fatalf("unexpected cue %+v", cues[0])
	}
}

func TestCuesFromSEIRBSP_survivesBufferReuse(t *testing.T) {
	items := []rawCue{{Sub: "owned", SubID: 3, OTMs: 0, DTMs: 500}}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	seiPayload := append(append([]byte{}, subtitleUUID...), payload...)
	nal := buildAVCSEINAL(seiPayload)
	au := accessUnit{PTSMs: 2000, Codec: flv.VideoKindAVC, NALs: nal}

	var owned []byte
	sei.ForEachRBSP(au.NALs, videoSEICodec(au.Codec), func(rbsp []byte) {
		owned = append([]byte(nil), rbsp...)
	})
	if len(owned) == 0 {
		t.Fatal("expected SEI RBSP")
	}

	cues := cuesFromSEIRBSP(au.PTSMs, owned)
	if len(cues) != 1 || cues[0].Text != "owned" {
		t.Fatalf("unexpected cues %+v", cues)
	}
	for i := range au.NALs {
		au.NALs[i] = 0xff
	}
	cues2 := cuesFromSEIRBSP(au.PTSMs, owned)
	if len(cues2) != 1 || cues2[0].Text != "owned" {
		t.Fatalf("cues changed after buffer corruption: %+v", cues2)
	}
}

func TestSegmentWriter_rejectsOversizedRBSP(t *testing.T) {
	w := newSegmentWriter(testSubtitleService(), filepath.Join(t.TempDir(), "x.flv"))
	bigPayload := make([]byte, maxSEIRBSPBytes+64)
	copy(bigPayload, subtitleUUID)
	au := accessUnit{PTSMs: 0, Codec: flv.VideoKindAVC, NALs: buildAVCSEINAL(bigPayload)}
	w.enqueueFromAccessUnit(au)
	if len(w.seen) != 0 || w.writer != nil {
		t.Fatalf("expected oversized RBSP to produce no cues")
	}
}

func TestFMP4StypPrefix(t *testing.T) {
	items := []rawCue{{Sub: "styp", SubID: 1, OTMs: 0, DTMs: 1000}}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	seiPayload := append(append([]byte{}, subtitleUUID...), payload...)
	sample := buildAVCSEINAL(seiPayload)

	moofMdat := buildMoofMdatSegment(1, sample)
	styp := appendISOBox("styp", []byte("isomisom"))
	seg := append(styp, moofMdat...)
	st := &fmp4State{VideoTrackID: 1, Timescale: 1000, Codec: flv.VideoKindAVC}
	aus := accessUnitsFromFMP4Chunk(seg, st)
	if len(aus) != 1 {
		t.Fatalf("expected 1 access unit through styp prefix, got %d", len(aus))
	}
	if cues := cuesFromSEI(aus[0]); len(cues) != 1 || cues[0].Text != "styp" {
		t.Fatalf("unexpected cues %+v", cues)
	}
}

func TestFMP4TrunDataOffset(t *testing.T) {
	items := []rawCue{{Sub: "fmp4", SubID: 2, OTMs: 0, DTMs: 1000}}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	seiPayload := append(append([]byte{}, subtitleUUID...), payload...)
	sample := buildAVCSEINAL(seiPayload)

	seg := buildMoofMdatSegment(1, sample)
	st := &fmp4State{VideoTrackID: 1, Timescale: 1000, Codec: flv.VideoKindAVC}
	aus := accessUnitsFromFMP4Chunk(seg, st)
	if len(aus) != 1 {
		t.Fatalf("expected 1 access unit, got %d", len(aus))
	}
	cues := cuesFromSEI(aus[0])
	if len(cues) != 1 || cues[0].Text != "fmp4" {
		t.Fatalf("unexpected cues %+v", cues)
	}
}

func buildMoofMdatSegment(trackID uint32, sample []byte) []byte {
	tfhd := appendISOBox("tfhd", []byte{
		0, 0, 0, 0,
		byte(trackID >> 24), byte(trackID >> 16), byte(trackID >> 8), byte(trackID),
	})
	tfdt := appendISOBox("tfdt", []byte{
		0, 0, 0, 0,
		0, 0, 0, 0,
	})
	trunBody := make([]byte, 16)
	trunBody[2] = 0x02
	trunBody[3] = 0x01 // data_offset + sample_size
	binary.BigEndian.PutUint32(trunBody[4:8], 1)
	binary.BigEndian.PutUint32(trunBody[12:16], uint32(len(sample)))

	trun := appendISOBox("trun", trunBody)
	traf := appendISOBox("traf", append(append(tfhd, tfdt...), trun...))
	moof := appendISOBox("moof", traf)
	mdatPayloadStart := len(moof) + 8
	dataOffset := uint32(mdatPayloadStart)
	binary.BigEndian.PutUint32(trunBody[8:12], dataOffset)
	trun = appendISOBox("trun", trunBody)
	traf = appendISOBox("traf", append(append(tfhd, tfdt...), trun...))
	moof = appendISOBox("moof", traf)
	mdat := appendISOBox("mdat", sample)
	return append(moof, mdat...)
}

func appendISOBox(typ string, body []byte) []byte {
	out := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(body)))
	out[4], out[5], out[6], out[7] = typ[0], typ[1], typ[2], typ[3]
	copy(out[8:], body)
	return out
}

func buildAVCSEINAL(payload []byte) []byte {
	// NAL type 6 + minimal SEI RBSP: type 5, size, payload, rbsp_trailing
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
	var out []byte
	ln := uint32(len(nal))
	out = append(out, byte(ln>>24), byte(ln>>16), byte(ln>>8), byte(ln))
	out = append(out, nal...)
	return out
}
