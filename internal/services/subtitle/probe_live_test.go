package subtitle

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/bilirec/bilirec/utils"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/hls"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const probeSubtitleRoomID = 22748536

func TestLiveProbeHLSFMP4SEI(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if os.Getenv("BILIREC_SUBTITLE_PROBE") != "1" {
		t.Skip("set BILIREC_SUBTITLE_PROBE=1 for live HLS probe")
	}
	probeHLSSEI(t, bilibili.ProfileHLSFMP4, func(seg []byte, st *fmp4State) (int, int) {
		aus := 0
		cues := 0
		for _, au := range accessUnitsFromFMP4Chunk(seg, st) {
			aus++
			cues += len(cuesFromSEI(au))
		}
		return aus, cues
	})
}

func TestLiveProbeHLSTSSEI(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if os.Getenv("BILIREC_SUBTITLE_PROBE") != "1" {
		t.Skip("set BILIREC_SUBTITLE_PROBE=1 for live HLS probe")
	}
	probeHLSSEI(t, bilibili.ProfileHLSTS, func(seg []byte, _ *fmp4State) (int, int) {
		sc := &tsScratch{}
		aus := 0
		cues := 0
		for _, au := range accessUnitsFromTSChunk(seg, sc) {
			aus++
			cues += len(cuesFromSEI(au))
		}
		return aus, cues
	})
}

func TestLiveFMP4SubtitleMerge(t *testing.T) {
	skipUnlessLiveSubtitleProbe(t)
	segments := captureHLSSegments(t, bilibili.ProfileHLSFMP4, "fmp4")
	runLiveSubtitleMerge(t, "fmp4", ".fmp4", "live.fmp4", segments, nil)
}

func TestLiveTSSubtitleMerge(t *testing.T) {
	skipUnlessLiveSubtitleProbe(t)
	segments := captureHLSSegments(t, bilibili.ProfileHLSTS, "ts")
	runLiveSubtitleMerge(t, "ts", ".ts", "live.ts", segments, nil)
}

func TestLiveFLVSubtitleMerge(t *testing.T) {
	skipUnlessLiveSubtitleProbe(t)

	var bili *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&bili),
	)
	app.RequireStart()
	defer app.RequireStop()

	enabled, err := bili.RoomSubtitleEnabled(probeSubtitleRoomID)
	if err != nil || !enabled {
		t.Skip("room subtitle not enabled")
	}
	urls, err := bili.GetStreamURLsV2(probeSubtitleRoomID, bilibili.WithProfiles(bilibili.ProfileHTTPFLV))
	if err != nil || len(urls) == 0 {
		t.Skipf("no flv urls: %v", err)
	}

	captureSec := probeCaptureSeconds()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(captureSec+30)*time.Second)
	defer cancel()
	resp, err := bili.FetchLiveStreamUrlWithCtx(urls[0].URL, ctx)
	if err != nil {
		t.Fatalf("fetch flv: %v", err)
	}
	if resp.StatusCode() != 200 {
		t.Fatalf("flv status %d", resp.StatusCode())
	}
	body := resp.RawBody()
	var raw bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(&raw, body)
	}()
	t.Logf("capturing live flv for %ds", captureSec)
	time.Sleep(time.Duration(captureSec) * time.Second)
	_ = body.Close()
	<-done

	flvBytes := raw.Bytes()
	if len(flvBytes) < 1024 {
		t.Fatalf("short flv capture: %d bytes", len(flvBytes))
	}
	chunks := splitCompleteFLVTags(flvBytes, 512<<10)
	var tags bytes.Buffer
	for _, chunk := range chunks {
		tags.Write(chunk)
	}

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "live.flv")
	if err := os.WriteFile(videoPath, flvBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := testSubtitleService()
	p := &segmentProcessor{svc: svc, videoPath: videoPath, ext: ".flv"}
	if err := p.Open(context.Background(), logger.Named("test")); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if _, err := p.Process(context.Background(), logger.Named("test"), chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	svc.Wait(videoPath)

	vttBytes, err := os.ReadFile(filepath.Join(dir, "live.vtt"))
	if err != nil {
		t.Fatalf("no vtt from live flv (%d bytes): %v", len(flvBytes), err)
	}
	vttTexts := vttCueTexts(string(vttBytes))
	order, first, last, events := seiTextsBySubID(tags.Bytes())
	assertLiveSubtitleMerge(t, "flv", len(flvBytes), events, vttTexts, order, first, last)
}

func pickHLSStreamURL(t *testing.T, bili *bilibili.Client, profile bilibili.StreamProfile, wantFormat string) (bilibili.StreamURLInfo, bool) {
	urls, err := bili.GetStreamURLsV2(
		probeSubtitleRoomID,
		bilibili.WithProfiles(profile),
		bilibili.WithQn(bilibili.QualityOriginal),
	)
	if err != nil || len(urls) == 0 {
		t.Skipf("no stream urls: %v", err)
		return bilibili.StreamURLInfo{}, false
	}
	var info bilibili.StreamURLInfo
	for _, u := range urls {
		if u.Format == wantFormat {
			info = u
			break
		}
	}
	if info.URL == "" {
		t.Skipf("no %s url in %d candidates", wantFormat, len(urls))
		return bilibili.StreamURLInfo{}, false
	}
	t.Logf("hls stream format=%s qn=%d codec=%s", info.Format, info.Qn, info.Codec)
	return info, true
}

func skipUnlessLiveSubtitleProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if os.Getenv("BILIREC_SUBTITLE_PROBE") != "1" {
		t.Skip("set BILIREC_SUBTITLE_PROBE=1 for live subtitle merge")
	}
}

func probeCaptureSeconds() int {
	captureSec := 90
	if s := os.Getenv("BILIREC_SUBTITLE_PROBE_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			captureSec = n
		}
	}
	return captureSec
}

func captureHLSSegments(t *testing.T, profile bilibili.StreamProfile, wantFormat string) [][]byte {
	t.Helper()
	var bili *bilibili.Client
	var st *stream.Service
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		metrics.Module,
		fx.Provide(notify.NewService),
		fx.Provide(stream.NewService),
		fx.Populate(&bili, &st),
	)
	app.RequireStart()
	defer app.RequireStop()

	enabled, err := bili.RoomSubtitleEnabled(probeSubtitleRoomID)
	if err != nil || !enabled {
		t.Skip("room subtitle not enabled")
	}
	info, ok := pickHLSStreamURL(t, bili, profile, wantFormat)
	if !ok {
		return nil
	}

	captureSec := probeCaptureSeconds()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(captureSec+90)*time.Second)
	defer cancel()
	chunkPool, release := st.AcquireChunkPool(info.Qn)
	defer release()

	ch, err := st.ReadHlsStream(
		0,
		func() (string, error) { return info.URL, nil },
		bili.GetLiveHlsPlaylistClient(),
		bili.GetLiveHlsSegmentClient(),
		ctx,
		info.Qn,
		chunkPool,
		release,
	)
	if err != nil {
		t.Fatalf("ReadHlsStream: %v", err)
	}

	t.Logf("capturing live hls %s for %ds", wantFormat, captureSec)
	deadline := time.After(time.Duration(captureSec) * time.Second)
	var segments [][]byte
	for {
		select {
		case seg, ok := <-ch:
			if !ok {
				if len(segments) == 0 {
					t.Fatal("hls stream ended before any segment")
				}
				return segments
			}
			segments = append(segments, append([]byte(nil), seg...))
		case <-deadline:
			if len(segments) == 0 {
				t.Fatal("no hls segments in capture window")
			}
			return segments
		}
	}
}

func runLiveSubtitleMerge(t *testing.T, label, ext, fileName string, chunks [][]byte, ref []byte) {
	t.Helper()
	var total int
	for _, c := range chunks {
		total += len(c)
	}
	dir := t.TempDir()
	videoPath := filepath.Join(dir, fileName)
	svc := testSubtitleService()
	p := &segmentProcessor{svc: svc, videoPath: videoPath, ext: ext}
	if err := p.Open(context.Background(), logger.Named("test")); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if _, err := p.Process(context.Background(), logger.Named("test"), chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	svc.Wait(videoPath)

	var order []int64
	var first, last map[int64]string
	var events int
	switch ext {
	case ".fmp4":
		order, first, last, events = seiTextsBySubIDFMP4(chunks)
	case ".ts":
		order, first, last, events = seiTextsBySubIDTS(chunks)
	default:
		if ref == nil {
			t.Fatalf("unsupported ext %s", ext)
		}
		order, first, last, events = seiTextsBySubID(ref)
	}
	_ = ref
	vttBytes, err := os.ReadFile(utils.SidecarPathForVideo(videoPath, ".vtt"))
	if err != nil {
		t.Fatalf("no vtt from live %s (%d bytes %d chunks sei_events=%d sei_ids=%d): %v",
			label, total, len(chunks), events, len(order), err)
	}
	vttTexts := vttCueTexts(string(vttBytes))
	assertLiveSubtitleMerge(t, label, total, events, vttTexts, order, first, last)
}

func assertLiveSubtitleMerge(t *testing.T, label string, totalBytes int, events int, vttTexts []string, order []int64, first, last map[int64]string) {
	t.Helper()
	t.Logf("%s=%d bytes sei_events=%d sei_ids=%d vtt_cues=%d", label, totalBytes, events, len(order), len(vttTexts))
	if len(order) == 0 {
		t.Fatalf("live %s had no subtitle SEI", label)
	}
	if len(vttTexts) != len(order) {
		t.Fatalf("cue count vtt=%d sei_ids=%d", len(vttTexts), len(order))
	}
	grew := 0
	matchedLast := 0
	shown := 0
	for i, id := range order {
		if last[id] != first[id] {
			grew++
			if shown < 5 {
				t.Logf("append id=%d first=%q last=%q vtt=%q", id, truncText(first[id]), truncText(last[id]), truncText(vttTexts[i]))
				shown++
			}
		}
		if vttTexts[i] == last[id] {
			matchedLast++
		} else {
			t.Errorf("id=%d vtt=%q first=%q last=%q", id, truncText(vttTexts[i]), truncText(first[id]), truncText(last[id]))
		}
	}
	t.Logf("ids text grew=%d vtt matches last=%d/%d", grew, matchedLast, len(order))
	if grew == 0 {
		t.Fatal("capture had no sub_id text updates; cannot show append merge")
	}
	if matchedLast != len(order) {
		t.Fatalf("vtt did not keep the last SEI text for every sub_id")
	}
}

func seiTextsBySubIDFMP4(segments [][]byte) (order []int64, first, last map[int64]string, events int) {
	first = make(map[int64]string)
	last = make(map[int64]string)
	var st fmp4State
	for _, seg := range segments {
		for _, au := range accessUnitsFromFMP4Chunk(seg, &st) {
			for _, c := range cuesFromSEI(au) {
				events++
				if _, ok := first[c.ID]; !ok {
					first[c.ID] = c.Text
					order = append(order, c.ID)
				}
				last[c.ID] = c.Text
			}
		}
	}
	return order, first, last, events
}

func seiTextsBySubIDTS(segments [][]byte) (order []int64, first, last map[int64]string, events int) {
	first = make(map[int64]string)
	last = make(map[int64]string)
	var sc tsScratch
	record := func(c cue) {
		events++
		if _, ok := first[c.ID]; !ok {
			first[c.ID] = c.Text
			order = append(order, c.ID)
		}
		last[c.ID] = c.Text
	}
	for _, seg := range segments {
		for _, au := range accessUnitsFromTSChunk(seg, &sc) {
			for _, c := range cuesFromSEI(au) {
				record(c)
			}
		}
	}
	if au, ok := flushTSPES(&sc); ok {
		for _, c := range cuesFromSEI(au) {
			record(c)
		}
	}
	return order, first, last, events
}

func splitCompleteFLVTags(data []byte, max int) [][]byte {
	rest := data
	var chunks [][]byte
	var cur []byte
	for len(rest) > 0 {
		tag, next, ok, skip := flv.NextTag(rest)
		if skip {
			rest = next
			continue
		}
		if !ok {
			break
		}
		rest = next
		if len(cur) > 0 && len(cur)+len(tag) > max {
			chunks = append(chunks, cur)
			cur = nil
		}
		cur = append(cur, tag...)
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

func seiTextsBySubID(tagBytes []byte) (order []int64, first, last map[int64]string, events int) {
	first = make(map[int64]string)
	last = make(map[int64]string)
	for _, au := range accessUnitsFromFLVChunk(tagBytes) {
		for _, c := range cuesFromSEI(au) {
			events++
			if _, ok := first[c.ID]; !ok {
				first[c.ID] = c.Text
				order = append(order, c.ID)
			}
			last[c.ID] = c.Text
		}
	}
	return order, first, last, events
}

func vttCueTexts(content string) []string {
	var texts []string
	for _, block := range strings.Split(content, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 2 || !strings.Contains(lines[0], "-->") {
			continue
		}
		texts = append(texts, strings.TrimSpace(strings.Join(lines[1:], "\n")))
	}
	return texts
}

func truncText(s string) string {
	r := []rune(s)
	if len(r) <= 24 {
		return s
	}
	return string(r[:24]) + "…"
}

func probeHLSSEI(t *testing.T, profile bilibili.StreamProfile, parse func([]byte, *fmp4State) (int, int)) {
	t.Helper()
	var bili *bilibili.Client
	var st *stream.Service
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		metrics.Module,
		fx.Provide(notify.NewService),
		fx.Provide(stream.NewService),
		fx.Populate(&bili, &st),
	)
	app.RequireStart()
	defer app.RequireStop()

	enabled, err := bili.RoomSubtitleEnabled(probeSubtitleRoomID)
	if err != nil || !enabled {
		t.Skip("room subtitle not enabled")
	}

	wantFormat := "fmp4"
	if profile == bilibili.ProfileHLSTS {
		wantFormat = "ts"
	}
	info, ok := pickHLSStreamURL(t, bili, profile, wantFormat)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	chunkPool, release := st.AcquireChunkPool(info.Qn)
	defer release()

	fetchM3u8 := func() (string, error) {
		return info.URL, nil
	}
	ch, err := st.ReadHlsStream(0, fetchM3u8, bili.GetLiveHlsPlaylistClient(), bili.GetLiveHlsSegmentClient(), ctx, info.Qn, chunkPool, release)
	if err != nil {
		t.Fatalf("ReadHlsStream: %v", err)
	}

	var state fmp4State
	var chunks, totalAU, totalCues, rawUUID int
	deadline := time.After(45 * time.Second)
	for {
		select {
		case seg, ok := <-ch:
			if !ok {
				t.Fatalf("stream ended early: chunks=%d aus=%d cues=%d rawUUID=%d state=%+v", chunks, totalAU, totalCues, rawUUID, state)
			}
			chunks++
			if bytes.Contains(seg, subtitleUUID) {
				rawUUID++
			}
			if chunks == 1 {
				parseInit(seg, &state)
				t.Logf("init track=%d timescale=%d trexSize=%v trexDur=%v moov=%s", state.VideoTrackID, state.Timescale, state.trexSize, state.trexDur, describeMoovTree(seg))
			}
			if chunks <= 3 {
				t.Logf("chunk %d len=%d boxes=%s", chunks, len(seg), describeTopBoxes(seg))
				if chunks == 2 {
					t.Logf("moof debug: %s", summarizeMoofs(seg))
					t.Logf("chunk2 embed cues=%d uuid=%t", len(cuesFromEmbeddedUUID(seg, 0)), bytes.Contains(seg, subtitleUUID))
				}
			}
			aus, cues := parse(seg, &state)
			totalAU += aus
			totalCues += cues
			if chunks%15 == 0 {
				t.Logf("progress chunks=%d aus=%d cues=%d rawUUID=%d track=%d codec=%v", chunks, totalAU, totalCues, rawUUID, state.VideoTrackID, state.Codec)
			}
			if totalCues > 0 {
				t.Logf("OK chunks=%d aus=%d cues=%d rawUUID=%d state=%+v", chunks, totalAU, totalCues, rawUUID, state)
				return
			}
		case <-deadline:
			t.Fatalf("no cues in window: chunks=%d aus=%d cues=%d rawUUID=%d state=%+v", chunks, totalAU, totalCues, rawUUID, state)
		}
	}
}

func summarizeMoofs(seg []byte) string {
	var parts []string
	for off := 0; off < len(seg); {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		moofEnd := off + size
		if typ != "moof" {
			off = moofEnd
			continue
		}
		mdat, _ := mdatPayloadAfter(seg, moofEnd)
		for trafOff := off + 8; trafOff < moofEnd; {
			trafSize, trafTyp, _, trafOK := hls.ReadBoxHeader(seg, trafOff)
			if !trafOK {
				break
			}
			trafEnd := trafOff + trafSize
			if trafTyp == "traf" {
				parts = append(parts, summarizeTraf(seg, trafOff, trafEnd, len(mdat)))
			}
			trafOff = trafEnd
		}
		off = moofEnd
	}
	return strings.Join(parts, " | ")
}

func summarizeTraf(seg []byte, trafStart, trafEnd int, mdatLen int) string {
	trackID := uint32(0)
	var trunFlags uint32
	var sampleCount int
	var dataOffset int32
	hasTrun := false
	for o := trafStart + 8; o < trafEnd; {
		sz, typ, hdr, ok := hls.ReadBoxHeader(seg, o)
		if !ok {
			break
		}
		e := o + sz
		switch typ {
		case "tfhd":
			if id, ok := hls.TfhdTrackID(seg, o, e); ok {
				trackID = id
			}
		case "trun":
			hasTrun = true
			body := seg[o+hdr : e]
			if len(body) >= 12 {
				trunFlags = uint32(body[0])<<16 | uint32(body[1])<<8 | uint32(body[2])
				sampleCount = int(binary.BigEndian.Uint32(body[4:8]))
				if trunFlags&0x1 != 0 && len(body) >= 12 {
					dataOffset = int32(binary.BigEndian.Uint32(body[8:12]))
				}
			}
		}
		o = e
	}
	if !hasTrun {
		return "traf(no-trun)"
	}
	return fmt.Sprintf("traf(id=%d n=%d flags=0x%06x off=%d mdat=%d)", trackID, sampleCount, trunFlags&0xffffff, dataOffset, mdatLen)
}

func describeMoovTree(seg []byte) string {
	for off := 0; off < len(seg); {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		end := off + size
		if typ == "moov" {
			return listBoxTypes(seg, off+8, end, 0)
		}
		off = end
	}
	return ""
}

func listBoxTypes(seg []byte, start, end int, depth int) string {
	if depth > 4 {
		return "..."
	}
	var parts []string
	for off := start; off < end; {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		boxEnd := off + size
		child := typ
		if typ == "mvex" || typ == "moov" || typ == "trak" {
			child += "{" + listBoxTypes(seg, off+8, boxEnd, depth+1) + "}"
		}
		parts = append(parts, child)
		off = boxEnd
	}
	return strings.Join(parts, ",")
}

func describeTopBoxes(seg []byte) string {
	var parts []string
	for off := 0; off < len(seg) && len(parts) < 6; {
		size, typ, _, ok := hls.ReadBoxHeader(seg, off)
		if !ok {
			break
		}
		parts = append(parts, typ)
		off += size
	}
	return strings.Join(parts, "+")
}
