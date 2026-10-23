//go:build integration

package preview_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pc "github.com/bilirec/bilirec/internal/controllers/preview"
	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/testutil/recording"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/gofiber/fiber/v3"
)

func TestPreviewMediaDuringRecord_FLV(t *testing.T) {
	runPreviewMediaDuringRecord(t, bilibili.ProfileHTTPFLV, "flv", "video/x-flv")
}

func TestPreviewMediaDuringRecord_TS(t *testing.T) {
	runPreviewMediaDuringRecord(t, bilibili.ProfileHLSTS, "ts", "video/mp2t")
}

func TestPreviewMediaDuringRecord_FMP4(t *testing.T) {
	runPreviewMediaDuringRecord(t, bilibili.ProfileHLSFMP4, "fmp4", "video/mp4")
}

func TestPreviewMediaAudioOnlyFLV(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping audio-only preview integration in short mode")
	}

	const readWindow = 45 * time.Second

	sess := recording.NewSession(t)
	roomID := recording.ResolveLiveTestRoomIDWithStream(t, sess,
		bilibili.WithProfiles(bilibili.ProfileHTTPFLV),
		bilibili.WithOnlyAudio(true),
	)
	baseURL := startPreviewHTTP(t, sess)

	startErr := sess.Recorder.Start(roomID, recorder.WithStreamOptions(
		bilibili.WithProfiles(bilibili.ProfileHTTPFLV),
		bilibili.WithOnlyAudio(true),
	))
	recording.HandleRecordingStartErr(t, startErr)
	recording.WaitForOutputPathAfterStart(t, sess.Recorder, roomID)

	deadline := time.Now().Add(readWindow)
	var soak flvSoak
	buf := make([]byte, 32*1024)
	gotBytes := 0

	for time.Now().Before(deadline) {
		if sess.Recorder.GetStatus(roomID) != recorder.Recording {
			t.Fatal("recording ended before audio-only preview validated")
		}
		resp := getPreviewMedia(t, context.Background(), baseURL, roomID, time.Until(deadline))
		if resp == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusConflict {
			resp.Body.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			t.Fatalf("GET /media status=%d body=%s", resp.StatusCode, body)
		}
		soak.resetBuf()
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				gotBytes += n
				soak.feed(buf[:n])
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Logf("preview media read: %v", err)
				break
			}
		}
		resp.Body.Close()
		if err := soak.okAudioOnly(); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := soak.okAudioOnly(); err != nil {
		t.Fatalf("audio-only FLV preview not valid after %s: %v (bytes=%d)", readWindow, err, gotBytes)
	}

	recording.StopRecording(t, sess.Recorder, roomID)
	recording.WaitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	time.Sleep(recording.SettleAfterStop)

	idle := getPreviewMedia(t, context.Background(), baseURL, roomID, 5*time.Second)
	if idle != nil {
		defer idle.Body.Close()
		if idle.StatusCode != http.StatusConflict {
			t.Fatalf("GET /media after stop: status=%d want %d", idle.StatusCode, http.StatusConflict)
		}
	}
}

func runPreviewMediaDuringRecord(t *testing.T, profile bilibili.StreamProfile, format, wantMIME string) {
	t.Helper()
	if testing.Short() {
		t.Skipf("skipping preview %s media soak in short mode", format)
	}

	sess := recording.NewSession(t)
	roomID := recording.ResolveLiveTestRoomIDWithStream(t, sess, bilibili.WithProfiles(profile))
	baseURL := startPreviewHTTP(t, sess)

	startErr := sess.Recorder.Start(roomID, recorder.WithStreamOptions(bilibili.WithProfiles(profile)))
	recording.HandleRecordingStartErr(t, startErr)
	outputPath := recording.WaitForOutputPathAfterStart(t, sess.Recorder, roomID)

	recordDuration := recording.IntegrationRecordDuration()
	t.Logf("preview media soak: room=%d format=%s duration=%s video=%s", roomID, format, recordDuration, outputPath)

	stats := soakPreviewMedia(t, sess, baseURL, roomID, format, wantMIME, recordDuration)
	t.Logf("preview media soak stats: bytes=%d reconnects=%d %s", stats.bytes, stats.reconnects, stats.detail())

	if stats.bytes == 0 {
		t.Fatalf("received no %s bytes from GET /media while recording", format)
	}
	if err := stats.ok(format); err != nil {
		t.Fatal(err)
	}

	recording.StopRecording(t, sess.Recorder, roomID)
	recording.WaitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	time.Sleep(recording.SettleAfterStop)

	idle := getPreviewMedia(t, context.Background(), baseURL, roomID, 5*time.Second)
	if idle != nil {
		defer idle.Body.Close()
		if idle.StatusCode != http.StatusConflict {
			t.Fatalf("GET /media after stop: status=%d want %d", idle.StatusCode, http.StatusConflict)
		}
	}

	if recording.CheckFFmpegAvailable(t) {
		recording.VerifyAllRecordingsInRoomDir(t, filepath.Dir(outputPath), format)
	}
}

func startPreviewHTTP(t *testing.T, sess *recording.Session) string {
	t.Helper()
	app := fiber.New()
	pc.NewController(app, sess.Preview)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen preview http: %v", err)
	}
	go func() {
		if serveErr := app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}); serveErr != nil {
			t.Logf("preview http stopped: %v", serveErr)
		}
	}()
	t.Cleanup(func() { _ = app.Shutdown() })
	return "http://" + ln.Addr().String()
}

type mediaSoakStats struct {
	bytes      int
	reconnects int
	flv        flvSoak
	ts         tsSoak
	fmp4       fmp4Soak
}

func (s mediaSoakStats) detail() string {
	return fmt.Sprintf("flv_tags=%d keyframes=%d headers=%d ts_packets=%d ftyp=%v moov=%v moof=%d",
		s.flv.tags, s.flv.keyframes, s.flv.headers, s.ts.packets, s.fmp4.ftyp, s.fmp4.moov, s.fmp4.moof)
}

func (s mediaSoakStats) ok(format string) error {
	switch format {
	case "flv":
		if !s.flv.sawHeader {
			return fmt.Errorf("FLV stream missing file header")
		}
		if s.flv.skip > 0 {
			return fmt.Errorf("FLV stream needed %d byte skips to resync", s.flv.skip)
		}
		if s.flv.headers == 0 {
			return fmt.Errorf("FLV stream missing video sequence header")
		}
		if s.flv.keyframes == 0 {
			return fmt.Errorf("FLV stream missing video keyframe")
		}
		if s.flv.tags < 8 {
			return fmt.Errorf("FLV stream too few tags: %d", s.flv.tags)
		}
	case "ts":
		if s.ts.packets < 50 {
			return fmt.Errorf("TS stream too few packets: %d", s.ts.packets)
		}
		if s.ts.badSync > 0 {
			return fmt.Errorf("TS stream had %d packets without 0x47 sync", s.ts.badSync)
		}
	case "fmp4":
		if !s.fmp4.ftyp {
			return fmt.Errorf("fMP4 stream missing ftyp")
		}
		if !s.fmp4.moov {
			return fmt.Errorf("fMP4 stream missing moov init")
		}
		if s.fmp4.moof == 0 {
			return fmt.Errorf("fMP4 stream missing moof")
		}
	}
	return nil
}

func soakPreviewMedia(t *testing.T, sess *recording.Session, baseURL string, roomID int, format, wantMIME string, d time.Duration) mediaSoakStats {
	t.Helper()
	deadline := time.Now().Add(d)
	var stats mediaSoakStats
	buf := make([]byte, 32*1024)

	for time.Now().Before(deadline) {
		if sess.Recorder.GetStatus(roomID) != recorder.Recording {
			t.Log("recording ended during preview soak")
			break
		}
		resp := getPreviewMedia(t, context.Background(), baseURL, roomID, time.Until(deadline))
		if resp == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusConflict {
			resp.Body.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			t.Fatalf("GET /media status=%d body=%s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, wantMIME) {
			resp.Body.Close()
			t.Fatalf("content-type=%s want %s", ct, wantMIME)
		}

		stats.flv.resetBuf()
		stats.ts.resetBuf()
		stats.fmp4.resetBuf()
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				stats.bytes += n
				switch format {
				case "flv":
					stats.flv.feed(buf[:n])
				case "ts":
					stats.ts.feed(buf[:n])
				case "fmp4":
					stats.fmp4.feed(buf[:n])
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Logf("preview media read: %v", err)
				break
			}
		}
		resp.Body.Close()
		if time.Now().Before(deadline) {
			stats.reconnects++
		}
	}
	return stats
}

func getPreviewMedia(t *testing.T, parent context.Context, baseURL string, roomID int, timeout time.Duration) *http.Response {
	t.Helper()
	if timeout <= 0 {
		timeout = time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/preview/rooms/%d/media", baseURL, roomID), nil)
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

type flvSoak struct {
	buf         []byte
	header      bool
	sawHeader   bool
	headerFlags byte
	tags        int
	keyframes   int
	headers     int
	audioSeq    int
	audioRaw    int
	skip        int
}

func (s *flvSoak) resetBuf() {
	s.buf = nil
	s.header = false
}

func (s *flvSoak) feed(p []byte) {
	s.buf = append(s.buf, p...)
	if !s.header {
		for {
			if len(s.buf) < flv.FlvHeaderSize {
				return
			}
			if string(s.buf[:3]) == "FLV" {
				break
			}
			s.skip++
			s.buf = s.buf[1:]
		}
		need := flv.FlvHeaderSize + flv.PrevTagSizeBytes
		if len(s.buf) < need {
			return
		}
		s.headerFlags = s.buf[4]
		s.buf = s.buf[need:]
		s.header = true
		s.sawHeader = true
	}
	for {
		tag, rest, ok, skip := flv.NextTag(s.buf)
		if skip {
			s.skip++
			s.buf = rest
			continue
		}
		if !ok {
			return
		}
		s.buf = rest
		s.tags++
		switch tag[0] {
		case flv.TagTypeVideo:
			_, isHeader, isKey := flv.ClassifyVideoTag(flv.TagData(tag))
			if isHeader {
				s.headers++
			}
			if isKey && !isHeader {
				s.keyframes++
			}
		case flv.TagTypeAudio:
			isAAC, isSeq := flv.ClassifyAudioTag(flv.TagData(tag))
			if isAAC && isSeq {
				s.audioSeq++
			} else if isAAC {
				s.audioRaw++
			}
		}
	}
}

func (s *flvSoak) okAudioOnly() error {
	if !s.sawHeader {
		return fmt.Errorf("FLV stream missing file header")
	}
	if s.skip > 0 {
		return fmt.Errorf("FLV stream needed %d byte skips to resync", s.skip)
	}
	if s.headerFlags != flv.TypeFlagAudio {
		return fmt.Errorf("TypeFlags=%#x want %#x", s.headerFlags, flv.TypeFlagAudio)
	}
	if s.audioSeq == 0 {
		return fmt.Errorf("FLV stream missing AAC sequence header")
	}
	if s.audioRaw < 3 {
		return fmt.Errorf("FLV stream too few AAC raw frames: %d", s.audioRaw)
	}
	if s.headers > 0 || s.keyframes > 0 {
		return fmt.Errorf("unexpected video tags seq=%d key=%d", s.headers, s.keyframes)
	}
	return nil
}

type tsSoak struct {
	buf     []byte
	synced  bool
	packets int
	badSync int
}

func (s *tsSoak) resetBuf() {
	s.buf = nil
	s.synced = false
}

func (s *tsSoak) feed(p []byte) {
	s.buf = append(s.buf, p...)
	if !s.synced {
		i := 0
		for i < len(s.buf) && s.buf[i] != 0x47 {
			i++
		}
		if i == len(s.buf) {
			s.buf = nil
			return
		}
		s.buf = s.buf[i:]
		s.synced = true
	}
	for len(s.buf) >= 188 {
		if s.buf[0] != 0x47 {
			s.badSync++
			s.synced = false
			s.buf = s.buf[1:]
			return
		}
		s.packets++
		s.buf = s.buf[188:]
	}
}

type fmp4Soak struct {
	buf  []byte
	ftyp bool
	moov bool
	moof int
}

func (s *fmp4Soak) resetBuf() {
	s.buf = nil
}

func (s *fmp4Soak) feed(p []byte) {
	s.buf = append(s.buf, p...)
	for {
		if len(s.buf) < 8 {
			return
		}
		size := uint64(binary.BigEndian.Uint32(s.buf[:4]))
		typ := string(s.buf[4:8])
		hdr := 8
		if size == 1 {
			if len(s.buf) < 16 {
				return
			}
			size = binary.BigEndian.Uint64(s.buf[8:16])
			hdr = 16
		}
		if size < uint64(hdr) {
			s.buf = s.buf[1:]
			continue
		}
		if uint64(len(s.buf)) < size {
			return
		}
		switch typ {
		case "ftyp":
			s.ftyp = true
		case "moov":
			s.moov = true
		case "moof":
			s.moof++
		}
		s.buf = s.buf[size:]
	}
}
