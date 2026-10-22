package preview

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

func TestJoinRequiresBoundPath(t *testing.T) {
	h := NewService()
	pipe := pipeline.New(h.Tap(8, "a.ts", ".ts"))
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	pipe.Close()
	if _, err := h.Subscribe(8); err != ErrNotRecording {
		t.Fatalf("err=%v", err)
	}
	if _, err := h.OpenMedia(8); err != ErrNotRecording {
		t.Fatalf("err=%v", err)
	}
}

func TestJoinWithoutBind(t *testing.T) {
	h := NewService()
	if _, err := h.Subscribe(1); err != ErrNotRecording {
		t.Fatalf("err=%v", err)
	}
}

func TestTapOpenProcessClose(t *testing.T) {
	h := NewService()
	info := h.Tap(4, "x.ts", ".ts")
	pipe := pipeline.New(info)
	if err := pipe.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	sub, err := h.Subscribe(4)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("s"), 10)
	if _, err := pipe.Process(context.Background(), chunk); err != nil {
		t.Fatal(err)
	}
	sub.Leave()
	pipe.Close()
	if _, err := h.Subscribe(4); err != ErrNotRecording {
		t.Fatal("unbind should clear path")
	}
}

func TestZeroSubscribersStillCapturesFmp4Init(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 2, "a.fmp4", ".fmp4")
	initSeg := ftypMoov(32)
	media := moofBox(48)
	mustProcess(t, pipe, initSeg)
	mustProcess(t, pipe, media)
	body, err := h.OpenMedia(2)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Body.Close()
	mustProcess(t, pipe, media)
	got := readSome(t, body.Body, len(initSeg)+len(media))
	if !bytes.HasPrefix(got, initSeg) {
		t.Fatalf("missing init: %d bytes", len(got))
	}
	if !bytes.Contains(got, media) {
		t.Fatal("missing moof")
	}
}

func TestFmp4InitAfterMediaSubscribe(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 2, "a.fmp4", ".fmp4")
	initSeg := ftypMoov(32)
	media := moofBox(48)
	body, err := h.OpenMedia(2)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Body.Close()
	mustProcess(t, pipe, initSeg)
	mustProcess(t, pipe, media)
	got := readSome(t, body.Body, len(initSeg)+len(media))
	if !bytes.HasPrefix(got, initSeg) {
		t.Fatalf("missing init: %d bytes", len(got))
	}
	if !bytes.Contains(got, media) {
		t.Fatal("missing moof")
	}
}

func TestTSViewerGetsPipelineBytes(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 7, "a.ts", ".ts")
	mustProcess(t, pipe, bytes.Repeat([]byte("a"), 40))
	media, err := h.OpenMedia(7)
	if err != nil {
		t.Fatal(err)
	}
	defer media.Body.Close()
	chunk := bytes.Repeat([]byte("b"), 50)
	mustProcess(t, pipe, chunk)
	got := readSome(t, media.Body, 50)
	if !bytes.Equal(got, chunk) {
		t.Fatalf("got %q want %q", got, chunk)
	}
}

func TestSlowViewerIsKicked(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 3, "a.ts", ".ts")
	media, err := h.OpenMedia(3)
	if err != nil {
		t.Fatal(err)
	}
	defer media.Body.Close()
	half := bytes.Repeat([]byte("a"), mediaMaxQueuedBytes/2)
	mustProcess(t, pipe, half)
	mustProcess(t, pipe, half)
	mustProcess(t, pipe, []byte("x"))
	got, err := io.ReadAll(media.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != mediaMaxQueuedBytes {
		t.Fatalf("queued %d, want %d", len(got), mediaMaxQueuedBytes)
	}
	if bytes.Contains(got, []byte("x")) {
		t.Fatal("overflow chunk must not be delivered")
	}
}

func TestFLVLateJoinStartsAtKeyframe(t *testing.T) {
	h := NewService()
	pipe := mustTap(t, h, 1, "a.flv", ".flv")
	seq := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01})
	audio := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x00, 0x12, 0x10})
	inter := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x27, 0x01, 0x00, 0x00, 0x00, 'E'})
	key := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 'K'})
	flv.SetTagTimestamp(inter, 1_000)
	flv.SetTagTimestamp(key, 60_000)

	var head bytes.Buffer
	head.Write(flv.NewFileHeaderBytes())
	head.Write(seq)
	head.Write(audio)
	mustProcess(t, pipe, head.Bytes())

	media, err := h.OpenMedia(1)
	if err != nil {
		t.Fatal(err)
	}
	defer media.Body.Close()
	mustProcess(t, pipe, inter)
	mustProcess(t, pipe, key)
	got := readSome(t, media.Body, 4096)
	if !bytes.HasPrefix(got, flv.NewFileHeaderBytes()) {
		t.Fatal("missing FLV file header")
	}
	if !bytes.Contains(got, seq) {
		t.Fatal("missing video sequence header")
	}
	if bytes.Contains(got, []byte{'E'}) {
		t.Fatal("inter frame must not appear before keyframe join")
	}
	if !bytes.Contains(got, []byte{'K'}) {
		t.Fatal("missing keyframe")
	}
	if ts := firstVideoMediaTS(got); ts != 0 {
		t.Fatalf("keyframe ts=%d want 0", ts)
	}
}

func TestPublishEventDroppedWhenFull(t *testing.T) {
	h := NewService()
	mustTap(t, h, 1, "a.ts", ".ts")
	sub, err := h.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Leave()
	ch := sub.C
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

func TestSubscribeOriginTracksSegmentStart(t *testing.T) {
	h := NewService()
	mustTap(t, h, 9, "a.ts", ".ts")
	sub, err := h.Subscribe(9)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Leave()
	if sub.origin < 0 {
		t.Fatal("clock")
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

func readSome(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	got, err := io.ReadAtLeast(r, buf, 1)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		t.Fatal(err)
	}
	return buf[:got]
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

func firstVideoMediaTS(buf []byte) uint32 {
	off := 0
	if len(buf) >= flv.FlvHeaderSize && string(buf[:3]) == "FLV" {
		off = flv.FlvHeaderSize + flv.PrevTagSizeBytes
	}
	for {
		tag, rest, ok, skip := flv.NextTag(buf[off:])
		if skip {
			off++
			continue
		}
		if !ok {
			return 0
		}
		if tag[0] == flv.TagTypeVideo {
			_, isHeader, isKey := flv.ClassifyVideoTag(flv.TagData(tag))
			if isKey && !isHeader {
				return flv.TagTimestamp(tag)
			}
		}
		off += len(buf[off:]) - len(rest)
	}
}
