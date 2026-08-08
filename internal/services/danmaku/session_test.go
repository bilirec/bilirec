package danmaku

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/puzpuzpuz/xsync/v4"
)

const testDanmakuJSON = `{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1754668800000,0],"测试<弹幕>&\"'",[123456,"测试用户"],0,0,0,0,0]}`

func TestMain(m *testing.M) {
	// danmaku getters fall back to defaults when the underlying fields are zero
	config.ReadOnly = config.NewGlobalReadOnlyForTest(false, 0)
	os.Exit(m.Run())
}

func newTestService() *Service {
	return &Service{
		outputFormat: "jsonl",
		sessions:     xsync.NewMap[int, *session](),
		pool:         pool.NewBytesPool(4096),
	}
}

func newTestSession(svc *Service, format string) (*session, string) {
	enc, err := NewFormatEncoder(format)
	if err != nil {
		panic(err)
	}
	meta := RoomMeta{RoomID: 12345, ShortID: 678, Uname: "测试主播", Title: "测试标题"}
	sess := newSession(123, meta, svc, enc, context.Background())
	return sess, filepath.Join(os.TempDir(), "bilirec-danmaku-test")
}

func makeFragment(t *testing.T, svc *Service, enc FormatEncoder, text string) []byte {
	t.Helper()
	raw := `{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1754668800000,0],"` + text + `",[123456,"用户"]]}`
	e, ok := parseDanmaku([]byte(raw))
	if !ok {
		t.Fatalf("failed to parse fragment for %q", text)
	}
	buf := svc.pool.GetBytes()
	frag := enc.AppendDanmaku(buf[:0], e, "0.001")
	if len(frag) == 0 {
		t.Fatalf("failed to build fragment for %q", text)
	}
	return frag
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertWellFormedXML(t *testing.T, content string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(content))
	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			t.Fatalf("XML not well-formed: %v\ncontent:\n%s", err, content)
		}
	}
}

func assertWellFormedJSONL(t *testing.T, content string) {
	t.Helper()
	for i, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("jsonl line %d invalid: %v\n%s", i, err, line)
		}
	}
}

func TestSessionWriteLoopLifecycleJSONL(t *testing.T) {
	svc := newTestService()
	sess, dir := newTestSession(svc, "jsonl")
	videoPath := filepath.Join(dir, t.Name(), "rec-0.flv")
	outPath := PathForVideo(videoPath, sess.encoder.Ext())
	defer os.RemoveAll(filepath.Join(dir, t.Name()))

	go sess.writeLoop(videoPath, time.Now())

	sess.msgCh <- makeFragment(t, svc, sess.encoder, "第一条弹幕")
	sess.msgCh <- makeFragment(t, svc, sess.encoder, "第二条弹幕")
	close(sess.msgCh)

	select {
	case <-sess.done:
	case <-time.After(5 * time.Second):
		t.Fatal("writeLoop did not finish after channel close")
	}

	content := readFile(t, outPath)
	assertWellFormedJSONL(t, content)
	for _, want := range []string{`"type":"meta"`, "第一条弹幕", "第二条弹幕", `"type":"danmaku"`} {
		if !strings.Contains(content, want) {
			t.Errorf("output missing %q:\n%s", want, content)
		}
	}
}

func TestSessionWriteLoopLifecycleXML(t *testing.T) {
	svc := newTestService()
	sess, dir := newTestSession(svc, "xml")
	videoPath := filepath.Join(dir, t.Name(), "rec-0.flv")
	outPath := PathForVideo(videoPath, sess.encoder.Ext())
	defer os.RemoveAll(filepath.Join(dir, t.Name()))

	go sess.writeLoop(videoPath, time.Now())

	sess.msgCh <- makeFragment(t, svc, sess.encoder, "第一条弹幕")
	sess.msgCh <- makeFragment(t, svc, sess.encoder, "第二条弹幕")
	close(sess.msgCh)

	select {
	case <-sess.done:
	case <-time.After(5 * time.Second):
		t.Fatal("writeLoop did not finish after channel close")
	}

	content := readFile(t, outPath)
	assertWellFormedXML(t, content)
	for _, want := range []string{"<i>\n", "第一条弹幕", "第二条弹幕", "</i>\n", `start_time=`} {
		if !strings.Contains(content, want) {
			t.Errorf("output missing %q:\n%s", want, content)
		}
	}
}

func TestSessionRotate(t *testing.T) {
	svc := newTestService()
	sess, dir := newTestSession(svc, "jsonl")
	base := filepath.Join(dir, t.Name())
	videoPath0 := filepath.Join(base, "rec-0.flv")
	videoPath1 := filepath.Join(base, "rec-1.flv")
	defer os.RemoveAll(base)

	go sess.writeLoop(videoPath0, time.Now())

	sess.msgCh <- makeFragment(t, svc, sess.encoder, "分段零弹幕")

	newStart := time.Now()
	sess.rotateCh <- rotateRequest{videoPath: videoPath1, segmentStart: newStart}

	deadline := time.Now().Add(5 * time.Second)
	for sess.segmentStartNano.Load() != newStart.UnixNano() {
		if time.Now().After(deadline) {
			t.Fatal("rotation was not processed in time")
		}
		time.Sleep(5 * time.Millisecond)
	}

	sess.msgCh <- makeFragment(t, svc, sess.encoder, "分段一弹幕")
	close(sess.msgCh)

	select {
	case <-sess.done:
	case <-time.After(5 * time.Second):
		t.Fatal("writeLoop did not finish after channel close")
	}

	content0 := readFile(t, PathForVideo(videoPath0, ".jsonl"))
	assertWellFormedJSONL(t, content0)
	if !strings.Contains(content0, "分段零弹幕") {
		t.Errorf("segment 0 file wrong:\n%s", content0)
	}
	if strings.Contains(content0, "分段一弹幕") {
		t.Errorf("segment 0 file contains post-rotation message:\n%s", content0)
	}

	content1 := readFile(t, PathForVideo(videoPath1, ".jsonl"))
	assertWellFormedJSONL(t, content1)
	if !strings.Contains(content1, "分段一弹幕") {
		t.Errorf("segment 1 file wrong:\n%s", content1)
	}
}

func TestSessionChannelFullDropsMessages(t *testing.T) {
	prev := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyWithDanmakuOverflowForTest("drop")
	t.Cleanup(func() { config.ReadOnly = prev })

	svc := newTestService()
	sess, _ := newTestSession(svc, "jsonl")

	raw := []byte(testDanmakuJSON)

	capacity := cap(sess.msgCh)
	total := capacity + 50
	for i := 0; i < total; i++ {
		sess.handleDanmaku(raw)
	}

	if got := sess.dropped.Load(); got != 50 {
		t.Errorf("dropped = %d, want 50", got)
	}
	if got := len(sess.msgCh); got != capacity {
		t.Errorf("queued = %d, want %d", got, capacity)
	}

	for i := 0; i < capacity; i++ {
		svc.pool.PutBytes(<-sess.msgCh)
	}
}

func TestSessionChannelFullBlocksWhenPolicyBlock(t *testing.T) {
	prev := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyWithDanmakuOverflowForTest("block")
	t.Cleanup(func() { config.ReadOnly = prev })

	svc := newTestService()
	sess, _ := newTestSession(svc, "jsonl")

	raw := []byte(testDanmakuJSON)
	capacity := cap(sess.msgCh)
	for i := 0; i < capacity; i++ {
		sess.handleDanmaku(raw)
	}
	if got := len(sess.msgCh); got != capacity {
		t.Fatalf("queued = %d, want %d", got, capacity)
	}

	blocked := make(chan struct{})
	go func() {
		sess.handleDanmaku(raw)
		close(blocked)
	}()

	select {
	case <-blocked:
		t.Fatal("block policy returned before channel had space")
	case <-time.After(50 * time.Millisecond):
	}

	svc.pool.PutBytes(<-sess.msgCh)

	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("block policy did not unblock after space freed")
	}
	if got := sess.dropped.Load(); got != 0 {
		t.Errorf("dropped = %d, want 0 under block policy", got)
	}
	for len(sess.msgCh) > 0 {
		svc.pool.PutBytes(<-sess.msgCh)
	}
}

func TestSessionChannelBlockRespectsCancel(t *testing.T) {
	prev := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyWithDanmakuOverflowForTest("block")
	t.Cleanup(func() { config.ReadOnly = prev })

	svc := newTestService()
	sess, _ := newTestSession(svc, "jsonl")
	raw := []byte(testDanmakuJSON)
	for i := 0; i < cap(sess.msgCh); i++ {
		sess.handleDanmaku(raw)
	}

	done := make(chan struct{})
	go func() {
		sess.handleDanmaku(raw)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	sess.cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("block enqueue did not return after session cancel")
	}
	if got := sess.dropped.Load(); got != 0 {
		t.Errorf("dropped = %d, want 0", got)
	}
}

func TestSessionSupervisorPanicRecovery(t *testing.T) {
	svc := newTestService()
	sess, dir := newTestSession(svc, "jsonl")
	videoPath := filepath.Join(dir, t.Name(), "rec-0.flv")
	defer os.RemoveAll(filepath.Join(dir, t.Name()))

	go sess.supervise()
	go sess.writeLoop(videoPath, time.Now())

	select {
	case <-sess.done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not terminate after supervisor panic")
	}

	content := readFile(t, PathForVideo(videoPath, ".jsonl"))
	assertWellFormedJSONL(t, content)
	if !strings.Contains(content, `"type":"meta"`) {
		t.Errorf("file not finalized after panic:\n%s", content)
	}
}

func TestSessionRepeatedStartStopNoLeak(t *testing.T) {
	svc := newTestService()
	base := filepath.Join(os.TempDir(), "bilirec-danmaku-test", t.Name())
	defer os.RemoveAll(base)

	runtime.GC()
	before := runtime.NumGoroutine()

	const cycles = 20
	for i := 0; i < cycles; i++ {
		sess, _ := newTestSession(svc, "jsonl")
		videoPath := filepath.Join(base, "rec-"+strings.Repeat("0", i%2)+".flv")
		go sess.writeLoop(videoPath, time.Now())
		sess.msgCh <- makeFragment(t, svc, sess.encoder, "x")
		close(sess.msgCh)
		select {
		case <-sess.done:
		case <-time.After(5 * time.Second):
			t.Fatalf("cycle %d: writeLoop did not finish", i)
		}
	}

	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Errorf("goroutines grew from %d to %d after %d cycles", before, after, cycles)
	}
}
