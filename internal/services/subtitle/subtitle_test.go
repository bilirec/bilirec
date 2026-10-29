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

func TestSegmentWriter_timelineAnchor(t *testing.T) {
	feed := func(w *segmentWriter, text string, id, pts, otMs int64) {
		t.Helper()
		w.enqueueFromAccessUnit(accessUnit{
			PTSMs: pts,
			Codec: flv.VideoKindAVC,
			NALs: buildAVCSEINAL(func() []byte {
				t.Helper()
				payload, err := json.Marshal([]rawCue{{Sub: text, SubID: id, OTMs: otMs, DTMs: 5000}})
				if err != nil {
					t.Fatal(err)
				}
				return append(append([]byte{}, subtitleUUID...), payload...)
			}()),
		})
	}

	tests := []struct {
		name string
		ext  string
		// 首个不带字幕的 AU（用于 TS 锚定，FLV/fMP4 忽略）
		firstAUPTSMs int64
		cues         []struct {
			text string
			id   int64
			pts  int64
			ot   int64
		}
		wantCue string
	}{
		{
			// FLV：上游 realtime_fixer 已归零，PTS 即文件内绝对时间，不扣任何基准。
			name: "flv uses pts as-is",
			ext:  ".flv",
			cues: []struct {
				text string
				id   int64
				pts  int64
				ot   int64
			}{
				{"第一句", 1, 20_000, 0},
				{"第二句", 2, 25_000, 0},
			},
			wantCue: "00:00:20.000 --> 00:00:25.000",
		},
		{
			// fMP4：上游 fmp4_timestamp_normalizer 已归零，与 FLV 相同语义。
			name: "fmp4 uses pts as-is",
			ext:  ".fmp4",
			cues: []struct {
				text string
				id   int64
				pts  int64
				ot   int64
			}{
				{"第一句", 1, 20_000, 0},
				{"第二句", 2, 25_000, 0},
			},
			wantCue: "00:00:20.000 --> 00:00:25.000",
		},
		{
			// TS：PES PTS 保留源 epoch，需减去首个视频 AU 的 PTS。
			name:         "ts anchors to first au pts",
			ext:          ".ts",
			firstAUPTSMs: 3_600_000,
			cues: []struct {
				text string
				id   int64
				pts  int64
				ot   int64
			}{
				{"第一句", 1, 3_605_000, 0},
				{"第二句", 2, 3_625_000, 0},
			},
			wantCue: "00:00:05.000 --> 00:00:25.000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			w := newSegmentWriter(testSubtitleService(), filepath.Join(dir, "seg"+tt.ext))
			w.enqueueFromAccessUnit(accessUnit{
				PTSMs:    tt.firstAUPTSMs,
				ptsKnown: tt.ext == ".ts", // 只有 .ts 需要「已解析的 PTS」才能當錨點
				Codec:    flv.VideoKindAVC,
				NALs:     buildAVCSEINAL([]byte{0x00}), // 无字幕的普通 SEI-less NAL
			})
			for _, c := range tt.cues {
				feed(w, c.text, c.id, c.pts, c.ot)
			}
			w.finish()

			data, err := os.ReadFile(filepath.Join(dir, "seg.vtt"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tt.wantCue) {
				t.Fatalf("expected %q in vtt: %q", tt.wantCue, data)
			}
		})
	}
}

func TestSegmentWriter_ignoresStaleReplayForEnd(t *testing.T) {
	dir := t.TempDir()
	w := newSegmentWriter(testSubtitleService(), filepath.Join(dir, "seg.flv"))
	feed := func(text string, id, pts, dt int64) {
		t.Helper()
		payload, err := json.Marshal([]rawCue{{Sub: text, SubID: id, OTMs: 0, DTMs: dt}})
		if err != nil {
			t.Fatal(err)
		}
		w.enqueueFromAccessUnit(accessUnit{
			PTSMs: pts,
			Codec: flv.VideoKindAVC,
			NALs:  buildAVCSEINAL(append(append([]byte{}, subtitleUUID...), payload...)),
		})
	}
	// 首次：start=10s，dt=2s。
	feed("嗯", 7, 10_000, 2_000)
	// 句末过期重发：start 后移到 13s，dt=8s。旧逻辑会把 endMs 拉到 21s。
	feed("嗯", 7, 13_000, 8_000)
	w.finish()

	data, err := os.ReadFile(filepath.Join(dir, "seg.vtt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "00:00:10.000 --> 00:00:12.000") {
		t.Fatalf("stale replay should not extend end: %q", data)
	}
}

func TestSegmentWriter_staleReplayStillUpdatesText(t *testing.T) {
	dir := t.TempDir()
	w := newSegmentWriter(testSubtitleService(), filepath.Join(dir, "seg.flv"))
	feed := func(text string, id, pts, dt int64) {
		t.Helper()
		payload, err := json.Marshal([]rawCue{{Sub: text, SubID: id, OTMs: 0, DTMs: dt}})
		if err != nil {
			t.Fatal(err)
		}
		w.enqueueFromAccessUnit(accessUnit{
			PTSMs: pts,
			Codec: flv.VideoKindAVC,
			NALs:  buildAVCSEINAL(append(append([]byte{}, subtitleUUID...), payload...)),
		})
	}
	// 句末重发：start 后移 3s（超过 staleReplayDriftMs），正文补全。
	feed("嗯", 7, 10_000, 2_000)
	feed("嗯你好", 7, 13_000, 8_000)
	w.finish()

	data, err := os.ReadFile(filepath.Join(dir, "seg.vtt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "嗯你好") {
		t.Fatalf("stale replay should still update text: %q", content)
	}
	if !strings.Contains(content, "00:00:10.000 --> 00:00:12.000") {
		t.Fatalf("stale replay should not extend end: %q", content)
	}
}

func TestSegmentWriter_ordersCuesByStart(t *testing.T) {
	dir := t.TempDir()
	w := newSegmentWriter(testSubtitleService(), filepath.Join(dir, "seg.flv"))
	feed := func(text string, id, pts, dt int64) {
		t.Helper()
		payload, err := json.Marshal([]rawCue{{Sub: text, SubID: id, OTMs: 0, DTMs: dt}})
		if err != nil {
			t.Fatal(err)
		}
		w.enqueueFromAccessUnit(accessUnit{
			PTSMs: pts,
			Codec: flv.VideoKindAVC,
			NALs:  buildAVCSEINAL(append(append([]byte{}, subtitleUUID...), payload...)),
		})
	}
	// 跨 sub_id 起點倒退：後到的 id=15 起點比前一條早 2.232s。
	feed("後到", 56, 80_656, 2_000)
	feed("先顯示", 15, 78_424, 2_000)
	w.finish()

	data, err := os.ReadFile(filepath.Join(dir, "seg.vtt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	first := "00:01:18.424 --> 00:01:20.656"
	second := "00:01:20.656 --> 00:01:22.656"
	for _, want := range []string{first, second, "先顯示", "後到"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected %q in vtt: %q", want, content)
		}
	}
	if strings.Contains(content, "00:01:20.656 --> 00:01:20.656") {
		t.Fatalf("earlier cue should not collapse to zero duration: %q", content)
	}
	if strings.Index(content, first) > strings.Index(content, second) {
		t.Fatalf("cues must be written in time order: %q", content)
	}
}

func TestSegmentWriter_keepsDurationWhenStartClamped(t *testing.T) {
	type feedCue struct {
		id, pts, dt int64
	}
	writeVTT := func(t *testing.T, cues []feedCue) string {
		t.Helper()
		dir := t.TempDir()
		w := newSegmentWriter(testSubtitleService(), filepath.Join(dir, "seg.flv"))
		for _, c := range cues {
			payload, err := json.Marshal([]rawCue{{Sub: "s", SubID: c.id, OTMs: 0, DTMs: c.dt}})
			if err != nil {
				t.Fatal(err)
			}
			w.enqueueFromAccessUnit(accessUnit{
				PTSMs: c.pts,
				Codec: flv.VideoKindAVC,
				NALs:  buildAVCSEINAL(append(append([]byte{}, subtitleUUID...), payload...)),
			})
		}
		w.finish()
		data, err := os.ReadFile(filepath.Join(dir, "seg.vtt"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	t.Run("sameClampedStartKeepsOwnEnd", func(t *testing.T) {
		// 首條起點 -200（顯示 0，自己的結束 1.8s），下一條也是 0：
		// 不能被下一條截成零時長。
		content := writeVTT(t, []feedCue{{1, -200, 2_000}, {2, 0, 1_000}})
		for _, want := range []string{
			"00:00:00.000 --> 00:00:01.800",
			"00:00:00.000 --> 00:00:01.000",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("expected %q in vtt: %q", want, content)
			}
		}
		if strings.Contains(content, "00:00:00.000 --> 00:00:00.000") {
			t.Fatalf("clamped start must not collapse to zero duration: %q", content)
		}
	})

	t.Run("laterCueStillTruncates", func(t *testing.T) {
		// 後面還有 500ms 的句子：前兩條都截到 500ms，不讓 1.8s 蓋住它。
		content := writeVTT(t, []feedCue{{1, -200, 2_000}, {2, 0, 1_000}, {3, 500, 1_000}})
		for _, want := range []string{
			"00:00:00.000 --> 00:00:00.500",
			"00:00:00.500 --> 00:00:01.500",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("expected %q in vtt: %q", want, content)
			}
		}
	})

	t.Run("cueEndingBeforeZeroStaysZero", func(t *testing.T) {
		// 整句都在 0 之前（-2000 + 500 = -1500）：仍是零時長，不憑空顯示。
		content := writeVTT(t, []feedCue{{4, -2_000, 500}, {5, 0, 1_000}})
		if !strings.Contains(content, "00:00:00.000 --> 00:00:00.000") {
			t.Fatalf("cue ending before zero should stay zero duration: %q", content)
		}
	})
}

func TestSegmentWriter_tsHoldsCuesUntilKnownAnchor(t *testing.T) {
	dir := t.TempDir()
	w := newSegmentWriter(testSubtitleService(), filepath.Join(dir, "seg.ts"))
	feed := func(text string, id, pts int64, known bool) {
		t.Helper()
		payload, err := json.Marshal([]rawCue{{Sub: text, SubID: id, OTMs: 0, DTMs: 1_000}})
		if err != nil {
			t.Fatal(err)
		}
		w.enqueueFromAccessUnit(accessUnit{
			PTSMs:    pts,
			ptsKnown: known,
			Codec:    flv.VideoKindAVC,
			NALs:     buildAVCSEINAL(append(append([]byte{}, subtitleUUID...), payload...)),
		})
	}
	anchor := func(pts int64) {
		t.Helper()
		w.enqueueFromAccessUnit(accessUnit{
			PTSMs:    pts,
			ptsKnown: true,
			Codec:    flv.VideoKindAVC,
			NALs:     buildAVCSEINAL([]byte{0x00}), // 無字幕 AU，只提供錨點
		})
	}
	// 首個 AU 的 PTS 還沒解析出來（0 佔位），不能當錨點。
	feed("先到的句子", 1, 0, false)
	anchor(3_600_000)
	feed("錨點後的句子", 2, 3_605_000, true)
	w.finish()

	data, err := os.ReadFile(filepath.Join(dir, "seg.vtt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// 若未知 PTS 被當成錨點（base=0），後一句會落在 01:00:05。
	if strings.Contains(content, "01:00:00") {
		t.Fatalf("unknown pts must not become the anchor: %q", content)
	}
	if !strings.Contains(content, "00:00:05.000 --> 00:00:06.000") {
		t.Fatalf("cue after anchor should subtract the first known pts: %q", content)
	}
	// 暫存 cue 的佔位 PTS（0）小於錨點，時間被 clamp 到 0，但文字仍保留。
	if !strings.Contains(content, "先到的句子") {
		t.Fatalf("held cue should still be written: %q", content)
	}
}

func TestSegmentWriter_extendsEndOnProgressiveGrowth(t *testing.T) {
	dir := t.TempDir()
	w := newSegmentWriter(testSubtitleService(), filepath.Join(dir, "seg.flv"))
	feed := func(text string, id, pts, dt int64) {
		t.Helper()
		payload, err := json.Marshal([]rawCue{{Sub: text, SubID: id, OTMs: 0, DTMs: dt}})
		if err != nil {
			t.Fatal(err)
		}
		w.enqueueFromAccessUnit(accessUnit{
			PTSMs: pts,
			Codec: flv.VideoKindAVC,
			NALs:  buildAVCSEINAL(append(append([]byte{}, subtitleUUID...), payload...)),
		})
	}
	// 同一句的渐进增长：start 不变，dt 变长。
	feed("嗯", 7, 10_000, 2_000)
	feed("嗯", 7, 10_000, 5_000)
	w.finish()

	data, err := os.ReadFile(filepath.Join(dir, "seg.vtt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "00:00:10.000 --> 00:00:15.000") {
		t.Fatalf("progressive growth should extend end: %q", data)
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
