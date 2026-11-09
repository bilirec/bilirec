package subtitle

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

// TestSegmentProcessor_panicStillRunsCleanup 是回归测试：RecoverPanic 必须由
// 被 defer 的函数直接调用（不能包进 cleanup closure 内层），否则 recover()
// 取不到 panic，panic 会炸穿 consume goroutine 使整个进程崩溃。
// 用未初始化的 index map（writeCue 对 nil map 赋值必 panic）作确定性注入点。
func TestSegmentProcessor_panicStillRunsCleanup(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "seg.flv")
	svc := testSubtitleService()

	buf := &bytes.Buffer{}
	logger.Init(logger.Options{Output: buf})
	t.Cleanup(func() {
		logger.SetOutput(io.Discard)
		logger.SetLevel(logger.InfoLevel)
	})

	p := &segmentProcessor{svc: svc, videoPath: videoPath, ext: ".flv"}
	if err := p.Open(context.Background(), logger.Named("test")); err != nil {
		t.Fatal(err)
	}

	// 注入 panic：把 seg 的 index 换成 nil map（白盒）。必须在 Push 之前改——
	// Push→Pop 自带 happens-before，consume 读到时不会与本写入竞争。
	// 下一个带 cue 的 chunk 会在 writeCue 里对 nil map 赋值而 panic。
	p.seg.index = nil
	seg := flvVideoChunkWithSEI(mustSubtitleJSON(t, "boom", 1))
	_, _ = p.Process(context.Background(), logger.Named("test"), seg)

	waitDone := make(chan struct{})
	go func() {
		svc.Wait(videoPath)
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("panic 后 cleanup 未执行：svc.Wait 卡住（done 未 close），说明 RecoverPanic 未生效")
	}

	// panic 后 idle 计数应归零：WaitIdle 不卡（等价于原计数归零断言）。
	idleDone := make(chan struct{})
	go func() {
		svc.WaitIdle()
		close(idleDone)
	}()
	select {
	case <-idleDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("panic 后 idle 计数未归零，WaitIdle 永久阻塞")
	}
	// 白盒确认 untrack 已执行（done 注册表已清空该 path）。
	svc.mu.Lock()
	_, stillTracked := svc.done[videoPath]
	svc.mu.Unlock()
	if stillTracked {
		t.Fatalf("panic 后未 untrack：%s 残留在注册表", videoPath)
	}

	// panic 已被 RecoverPanic 接住并记录（含调用栈），而不是炸穿 goroutine。
	p.q.Close()
	p.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "subtitle scan（path=") {
		if time.Now().After(deadline) {
			t.Fatalf("未见到 RecoverPanic 日志（panic 可能炸穿了进程）：buf=%q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "TestSegmentProcessor_panicStillRunsCleanup") {
		t.Fatalf("panic 日志缺堆栈帧：%q", buf.String())
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
