package subtitle

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/sei"
)

func TestCuesFromEmbeddedUUID_trailingBinary(t *testing.T) {
	items := []rawCue{{Sub: "trail", SubID: 3, OTMs: 0, DTMs: 500}}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	blob := append(append([]byte{}, subtitleUUID...), payload...)
	blob = append(blob, 0x80, 0x00, 0xff)
	cues := cuesFromEmbeddedUUID(blob, 42)
	if len(cues) != 1 || cues[0].Text != "trail" || cues[0].PTSMs != 42 {
		t.Fatalf("unexpected cues %+v", cues)
	}
}

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
	if len(w.cues) != 0 || w.writer != nil {
		t.Fatalf("expected oversized RBSP to produce no cues")
	}
}

func TestSegmentWriter_mergesSameSubID(t *testing.T) {
	dir := t.TempDir()
	w := newSegmentWriter(testSubtitleService(), filepath.Join(dir, "seg.flv"))
	feed := func(text string, id, pts int64) {
		t.Helper()
		au := accessUnit{
			PTSMs: pts,
			Codec: flv.VideoKindAVC,
			NALs:  buildAVCSEINAL(mustSubtitleJSON(t, text, id)),
		}
		w.enqueueFromAccessUnit(au)
	}
	feed("嗯", 7, 0)
	feed("谢谢你", 7, 400)
	feed("下一句", 8, 2000)
	w.finish()

	data, err := os.ReadFile(filepath.Join(dir, "seg.vtt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Count(content, "-->") != 2 {
		t.Fatalf("expected 2 cues, got %q", content)
	}
	if !strings.Contains(content, "谢谢你") || !strings.Contains(content, "下一句") {
		t.Fatalf("missing merged text: %q", content)
	}
	if strings.Contains(content, "嗯") {
		t.Fatalf("kept first SEI text: %q", content)
	}
	if !strings.Contains(content, "00:00:00.000 --> 00:00:02.000") {
		t.Fatalf("expected first cue truncated at next start: %q", content)
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

// PTS must be derived from the video track's own timescale. Using the audio
// timescale (44100) instead of the video one (12800) scales PTS by ~0.29.
func TestFMP4PerTrackTimescaleDrivesPTS(t *testing.T) {
	items := []rawCue{{Sub: "timescale", SubID: 4, OTMs: 0, DTMs: 1000}}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	seiPayload := append(append([]byte{}, subtitleUUID...), payload...)
	sample := buildAVCSEINAL(seiPayload)

	// 2s of video at 12800 ticks/s.
	const videoTS = 12800
	const audioTS = 44100
	const tfdt = 2 * videoTS

	seg := buildMoofMdatSegmentWithTfdt(1, tfdt, sample)
	st := &fmp4State{
		VideoTrackID:    1,
		Timescale:       audioTS, // legacy max-timescale value; must not win
		TrackTimescales: map[uint32]uint32{1: videoTS, 2: audioTS},
		Codec:           flv.VideoKindAVC,
	}
	aus := accessUnitsFromFMP4Chunk(seg, st)
	if len(aus) != 1 {
		t.Fatalf("expected 1 access unit, got %d", len(aus))
	}
	if aus[0].PTSMs != 2000 {
		t.Fatalf("PTS = %d ms, want 2000 (video timescale %d, not audio %d)",
			aus[0].PTSMs, videoTS, audioTS)
	}
	if cues := cuesFromSEI(aus[0]); len(cues) != 1 || cues[0].PTSMs != 2000 {
		t.Fatalf("unexpected cues %+v", cues)
	}
}

// Without a video entry the fallback Timescale applies, so subtitle PTS keeps
// working for init segments that never yielded a per-track map.
func TestFMP4TimescaleFallsBackWhenTrackMissing(t *testing.T) {
	st := &fmp4State{
		VideoTrackID:    1,
		Timescale:       90000,
		TrackTimescales: map[uint32]uint32{2: 44100},
		Codec:           flv.VideoKindAVC,
	}
	if got := st.timescaleForTrack(1); got != 90000 {
		t.Fatalf("fallback timescale = %d, want 90000", got)
	}
	if got := st.timescaleForTrack(2); got != 44100 {
		t.Fatalf("mapped timescale = %d, want 44100", got)
	}
	if got := st.timescaleForTrack(3); got != 90000 {
		t.Fatalf("unknown track should use fallback, got %d", got)
	}
}

func buildMoofMdatSegment(trackID uint32, sample []byte) []byte {
	return buildMoofMdatSegmentWithTfdt(trackID, 0, sample)
}

func buildMoofMdatSegmentWithTfdt(trackID uint32, tfdtBase uint64, sample []byte) []byte {
	tfhd := appendISOBox("tfhd", []byte{
		0, 0, 0, 0,
		byte(trackID >> 24), byte(trackID >> 16), byte(trackID >> 8), byte(trackID),
	})
	tfdt := appendISOBox("tfdt", []byte{
		1, 0, 0, 0, // version 1: 64-bit baseMediaDecodeTime
		byte(tfdtBase >> 56), byte(tfdtBase >> 48), byte(tfdtBase >> 40), byte(tfdtBase >> 32),
		byte(tfdtBase >> 24), byte(tfdtBase >> 16), byte(tfdtBase >> 8), byte(tfdtBase),
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
