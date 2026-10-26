package subtitle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/logger"
)

func testSubtitleService() *Service {
	return &Service{
		writer: sidecarWriterOpts{
			bufferSize:            65536,
			flushPeriod:           100 * time.Millisecond,
			syncPeriod:            time.Second,
			minPeriodicFlushBytes: 0,
			sequentialWrite:       false,
		},
		done: make(map[string]chan struct{}),
	}
}

func TestApplyChunkGap_clearsPESKeepsPID(t *testing.T) {
	ts := tsScratch{videoPID: 0x101, codec: flv.VideoKindAVC, havePES: true, pesBuf: []byte{1, 2, 3}}
	applyChunkGap(&ts)
	if ts.havePES || len(ts.pesBuf) != 0 {
		t.Fatalf("expected PES cleared, havePES=%v pesBuf=%v", ts.havePES, ts.pesBuf)
	}
	if ts.videoPID != 0x101 || ts.codec != flv.VideoKindAVC {
		t.Fatalf("expected PID/codec kept: pid=%d codec=%v", ts.videoPID, ts.codec)
	}
}

func TestSegmentProcessor_processCopiesChunk(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "seg.flv")
	svc := testSubtitleService()
	p := &segmentProcessor{svc: svc, videoPath: videoPath, ext: ".flv"}
	if err := p.Open(context.Background(), logger.Named("test")); err != nil {
		t.Fatal(err)
	}

	seg := flvVideoChunkWithSEI(mustSubtitleJSON(t, "copy", 1))
	owned := append([]byte(nil), seg...)
	_, err := p.Process(context.Background(), logger.Named("test"), owned)
	if err != nil {
		t.Fatal(err)
	}
	for i := range owned {
		owned[i] = 0
	}

	p.Close()
	svc.Wait(videoPath)

	vtt := filepath.Join(dir, "seg.vtt")
	data, err := os.ReadFile(vtt)
	if err != nil {
		t.Fatalf("read vtt: %v", err)
	}
	if !strings.Contains(string(data), "copy") {
		t.Fatalf("expected cue text in vtt: %q", data)
	}
}

func TestSegmentProcessor_closeReturnsBeforeDone(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "seg.flv")
	svc := testSubtitleService()
	p := &segmentProcessor{svc: svc, videoPath: videoPath, ext: ".flv"}
	if err := p.Open(context.Background(), logger.Named("test")); err != nil {
		t.Fatal(err)
	}
	seg := flvVideoChunkWithSEI(mustSubtitleJSON(t, "async", 2))
	_, _ = p.Process(context.Background(), logger.Named("test"), seg)

	closeDone := make(chan struct{})
	go func() {
		_ = p.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked")
	}

	waitDone := make(chan struct{})
	go func() {
		svc.Wait(videoPath)
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not finish")
	}
}

func flvVideoChunkWithSEI(seiPayload []byte) []byte {
	nal := buildAVCSEINAL(seiPayload)
	body := []byte{0x17, 0x01, 0, 0, 0}
	body = append(body, nal...)
	return flv.NewTagBytes(flv.TagTypeVideo, body)
}

func mustSubtitleJSON(t *testing.T, text string, id int64) []byte {
	t.Helper()
	items := []rawCue{{Sub: text, SubID: id, OTMs: 0, DTMs: 1000}}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte{}, subtitleUUID...), payload...)
}
