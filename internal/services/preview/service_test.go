package preview

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/pkg/pipeline"
)

func TestObserveNoViewersDoesNotIndex(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 1, "a.fmp4", ".fmp4")
	mustProcess(t, pipe, bytes.Repeat([]byte("x"), 1024))
	v, ok := h.roomView(1)
	if !ok {
		t.Fatal("missing room")
	}
	if v.offset != 1024 {
		t.Fatalf("offset=%d", v.offset)
	}
	if len(v.segs) != 0 {
		t.Fatalf("indexed %d segments with 0 viewers", len(v.segs))
	}
}

func TestObserveIndexesAfterJoin(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 7, "a.ts", ".ts")
	mustProcess(t, pipe, bytes.Repeat([]byte("a"), 100))
	_, leave, err := h.Subscribe(7)
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	mustProcess(t, pipe, bytes.Repeat([]byte("b"), 50))
	v, _ := h.roomView(7)
	if v.offset != 150 {
		t.Fatalf("offset=%d", v.offset)
	}
	if v.previewStart != 100 {
		t.Fatalf("previewStart=%d want 100", v.previewStart)
	}
	if len(v.segs) != 1 || v.segs[0].offset != 100 || v.segs[0].size != 50 {
		t.Fatalf("segs=%+v", v.segs)
	}
}

func TestRejoinStartsAtCurrentOffset(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 5, "a.ts", ".ts")
	_, leave, err := h.Subscribe(5)
	if err != nil {
		t.Fatal(err)
	}
	mustProcess(t, pipe, bytes.Repeat([]byte("a"), 40))
	leave()
	mustProcess(t, pipe, bytes.Repeat([]byte("b"), 60))
	_, leave2, err := h.Subscribe(5)
	if err != nil {
		t.Fatal(err)
	}
	defer leave2()
	mustProcess(t, pipe, bytes.Repeat([]byte("c"), 10))
	v, _ := h.roomView(5)
	if v.offset != 110 {
		t.Fatalf("offset=%d", v.offset)
	}
	if v.previewStart != 100 {
		t.Fatalf("previewStart=%d want 100", v.previewStart)
	}
	if len(v.segs) != 1 || v.segs[0].offset != 100 {
		t.Fatalf("segs=%+v", v.segs)
	}
}

func TestJoinRequiresBoundPath(t *testing.T) {
	h := NewService()
	pipe := pipeline.New(h.Tap(8, "a.ts", ".ts"))
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	pipe.Close()
	if _, _, err := h.Subscribe(8); err != ErrNotRecording {
		t.Fatalf("err=%v", err)
	}
}

func TestLastViewerClearsSegments(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 3, "a.ts", ".ts")
	_, leave, err := h.Subscribe(3)
	if err != nil {
		t.Fatal(err)
	}
	mustProcess(t, pipe, bytes.Repeat([]byte("a"), 20))
	leave()
	v, ok := h.roomView(3)
	if !ok {
		t.Fatal("room should remain while bound")
	}
	if len(v.segs) != 0 {
		t.Fatalf("segs leftover: %d", len(v.segs))
	}
	if v.previewStart != 0 {
		t.Fatalf("previewStart leftover: %d", v.previewStart)
	}
}

func TestPlaylistSkipsUnflushedAndOmitsEndlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seg.ts")
	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, 80), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewService()
	pipe := mustTap(t, h, 9, path, ".ts")
	_, leave, err := h.Subscribe(9)
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	mustProcess(t, pipe, bytes.Repeat([]byte{1}, 40))
	mustProcess(t, pipe, bytes.Repeat([]byte{1}, 40))
	mustProcess(t, pipe, bytes.Repeat([]byte{1}, 40)) // not on disk yet
	body, err := h.Playlist(9)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "#EXT-X-ENDLIST") {
		t.Fatal(body)
	}
	if !strings.Contains(body, "#EXT-X-PLAYLIST-TYPE:EVENT") {
		t.Fatal(body)
	}
	if strings.Count(body, "#EXTINF") != 2 {
		t.Fatalf("want 2 flushed segments, got\n%s", body)
	}
}

func TestFmp4PlaylistMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.fmp4")
	initSeg := ftypMoov(32)
	media := moofBox(48)
	if err := os.WriteFile(path, append(append([]byte{}, initSeg...), media...), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewService()
	pipe := mustTap(t, h, 2, path, ".fmp4")
	_, leave, err := h.Subscribe(2)
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	mustProcess(t, pipe, initSeg)
	mustProcess(t, pipe, media)
	body, err := h.Playlist(2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "media?offset=0&length="+itoa(len(initSeg))) {
		t.Fatal(body)
	}
	if strings.Count(body, "#EXTINF") != 1 {
		t.Fatal(body)
	}
}

func TestTapOpenProcessClose(t *testing.T) {
	h := NewService()
	info := h.Tap(4, "x.ts", ".ts")
	pipe := pipeline.New(info)
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, leave, err := h.Subscribe(4)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("s"), 10)
	if _, err := pipe.Process(context.Background(), chunk); err != nil {
		t.Fatal(err)
	}
	leave()
	pipe.Close()
	v, ok := h.roomView(4)
	if ok && v.path != "" {
		t.Fatal("unbind should clear path")
	}
}

func TestCopyRangeDoesNotBufferWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	src := bytes.Repeat([]byte{7}, 1000)
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := copyRange(&out, path, 10, 20); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), src[10:30]) {
		t.Fatal(out.Bytes())
	}
}

func TestFLVRangeReadsFileStartAfterLateJoin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.bin")
	src := bytes.Repeat([]byte{0x11}, 49)
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewService()
	pipe := mustTap(t, h, 1, path, ".flv")
	mustProcess(t, pipe, src)
	_, leave, err := h.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer leave()

	media, err := h.OpenMedia(context.Background(), 1, 0, 0, "bytes=0-8")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Body.Close()
	got, err := io.ReadAll(media.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src[:9]) {
		t.Fatalf("got %x want %x", got, src[:9])
	}
}

func TestCatchupDropsIncompleteLastLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"danmaku\"}\n{\"type\":\"gif"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := NewService().Catchup(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines", len(lines))
	}
}

func TestJoinWithoutBind(t *testing.T) {
	h := NewService()
	if _, _, err := h.Subscribe(1); err != ErrNotRecording {
		t.Fatalf("err=%v", err)
	}
}

func TestPublishEventDroppedWhenFull(t *testing.T) {
	h := NewService()
	mustTap(t, h, 1, "a.ts", ".ts")
	ch, unsub, err := h.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	for i := 0; i < eventChanSize+5; i++ {
		h.PublishEvent(1, []byte(`{"type":"danmaku"}`))
	}
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			if n == 0 || n > eventChanSize {
				t.Fatalf("drained %d", n)
			}
			return
		}
	}
}

func mustTap(t *testing.T, h *Service, roomID int, path, ext string) *pipeline.Pipe[[]byte] {
	t.Helper()
	pipe := pipeline.New(h.Tap(roomID, path, ext))
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pipe.Close)
	return pipe
}

func mustProcess(t *testing.T, pipe *pipeline.Pipe[[]byte], data []byte) {
	t.Helper()
	if _, err := pipe.Process(context.Background(), data); err != nil {
		t.Fatal(err)
	}
}

func ftypMoov(moovSize int) []byte {
	ftyp := box("ftyp", make([]byte, 8))
	moov := box("moov", make([]byte, moovSize))
	return append(ftyp, moov...)
}

func moofBox(n int) []byte {
	return box("moof", make([]byte, n))
}

func box(typ string, payload []byte) []byte {
	size := 8 + len(payload)
	b := make([]byte, size)
	b[0] = byte(size >> 24)
	b[1] = byte(size >> 16)
	b[2] = byte(size >> 8)
	b[3] = byte(size)
	copy(b[4:8], typ)
	copy(b[8:], payload)
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [16]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}
