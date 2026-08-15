package danmaku

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/sirupsen/logrus"
)

// Danmaku recording performance tests. These are the regression metrics for
// parse/encode/write CPU, heap retention, and goroutine cleanup.
//
//	go test ./internal/services/danmaku -count=1 -timeout 10m
//	go test ./internal/services/danmaku -bench=. -benchmem -count=1
//
// Passing tests log lines prefixed with "METRIC " for later baseline comparison.

const (
	danmakuPerfPoolSize           = 4096
	danmakuPerfHighRateMessages   = 50_000
	danmakuPerfFloodMessages      = 20_000
	danmakuPerfRotateRounds       = 10
	danmakuPerfRotateBatch        = 200
	danmakuPerfStartStopCycles    = 30
	danmakuPerfConcurrentSessions = 4
	danmakuPerfConcurrentMessages = 8_000

	// Regression guards. These are intentionally loose so CI noise does not
	// fail the job; tighten from METRIC logs once a baseline exists.
	danmakuPerfMaxRetainedAllocMB   = 8.0
	danmakuPerfMaxFloodRetainedMB   = 6.0
	danmakuPerfMaxGoroutineGrowth   = 6
	danmakuPerfMinMessagesPerSec    = 2_000
	danmakuPerfMaxParseAllocs       = 80.0
	danmakuPerfMaxXMLEncodeAllocs   = 12.0
	danmakuPerfMaxJSONLEncodeAllocs = 40.0
	danmakuPerfMaxHandleAllocs      = 150.0
	danmakuPerfMaxGiftParseAllocs   = 160.0
)

// Busy-room shaped DANMU_MSG: medals, extra JSON, and CJK text.
const perfDanmakuJSON = `{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1754668800000,0,0,"",0,0,0,0,0,{"url":"https://example.com/e.png","emoticon_unique":"room_1_emoji"},0,{"extra":"{\"dm_type\":0,\"direction\":0,\"emoticon_unique\":\"\"}"}],"今晚也来看直播啦",[123456789,"测试用户名",0,0,0,10000,1,0],[3,"粉丝牌","主播名",445566,16777215,0,0,0,0,0,0,0,987654],[1,0,16777215,"",0],[20,0,5805790,0],0,0,{"ts":1754668800,"ct":"ABCD"},0,0,0,0]}`

const perfGiftJSON = `{"cmd":"SEND_GIFT","data":{"uname":"礼物用户","uid":456,"giftName":"小花花","num":2,"price":100,"coin_type":"gold","face":"https://example.com/f.png","name_color":"#ffffff","action":"投喂","giftId":1,"giftType":0,"timestamp":1754668800}}`

const perfSuperChatJSON = `{"cmd":"SUPER_CHAT_MESSAGE","data":{"uid":123,"id":999,"start_time":1700000000,"end_time":1700000060,"user_info":{"uname":"sc用户","name_color":"#646c7a","face":"https://example.com/a.png"},"price":30,"time":60,"message":"醒目留言","background_color":"#EDF5FF","background_bottom_color":"#2A60B2","background_price_color":"#7497CD","message_font_color":"#FFFFFF","background_image":"https://example.com/bg.png"}}`

const perfGuardJSON = `{"cmd":"GUARD_BUY","data":{"username":"舰长用户","uid":789,"guard_level":3,"num":1,"price":198000,"gift_name":"舰长"}}`

func quietDanmakuLogs(t testing.TB) {
	t.Helper()
	prev := logrus.GetLevel()
	logrus.SetLevel(logrus.ErrorLevel)
	t.Cleanup(func() { logrus.SetLevel(prev) })
}

func useDanmakuOverflowPolicy(t testing.TB, policy string) {
	t.Helper()
	prev := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyWithDanmakuOverflowForTest(policy)
	t.Cleanup(func() { config.ReadOnly = prev })
}

func snapshotHeap(runGC bool) (allocMB, sysMB float64, goroutines int) {
	if runGC {
		runtime.GC()
		runtime.GC()
		debug.FreeOSMemory()
		time.Sleep(80 * time.Millisecond)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.Alloc) / (1024 * 1024), float64(ms.Sys) / (1024 * 1024), runtime.NumGoroutine()
}

func logDanmakuMetric(t testing.TB, name string, fields map[string]any) {
	t.Helper()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := name
	for _, k := range keys {
		buf += fmt.Sprintf(" %s=%v", k, fields[k])
	}
	t.Logf("METRIC %s", buf)
}

func waitSessionDone(t *testing.T, sess *session, timeout time.Duration) {
	t.Helper()
	select {
	case <-sess.done:
	case <-time.After(timeout):
		t.Fatal("writeLoop did not finish")
	}
}

func waitActiveSessions(t *testing.T, svc *Service, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if svc.ActiveSessions() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("active danmaku sessions = %d, want %d", svc.ActiveSessions(), want)
}

func countJSONLLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sc.Text() != "" {
			n++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return n
}

func startWriteLoop(t *testing.T, sess *session, videoPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(videoPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	go sess.writeLoop(videoPath, time.Now())
	time.Sleep(20 * time.Millisecond)
}

func waitForRotation(t *testing.T, sess *session, want time.Time, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	wantNano := want.UnixNano()
	for sess.segmentStartNano.Load() != wantNano {
		if time.Now().After(deadline) {
			t.Fatalf("rotation to %s was not processed in time", want.Format(time.RFC3339Nano))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestParseDanmaku_AllocsPerOp(t *testing.T) {
	raw := []byte(perfDanmakuJSON)
	if _, ok := parseDanmaku(raw); !ok {
		t.Fatal("perf payload failed to parse")
	}
	allocs := testing.AllocsPerRun(1000, func() {
		_, _ = parseDanmaku(raw)
	})
	logDanmakuMetric(t, "parse_danmaku_allocs", map[string]any{"allocs_per_op": fmt.Sprintf("%.1f", allocs)})
	if allocs > danmakuPerfMaxParseAllocs {
		t.Errorf("parseDanmaku allocs/op = %.1f, want <= %.1f", allocs, danmakuPerfMaxParseAllocs)
	}
}

func TestParseGift_AllocsPerOp(t *testing.T) {
	raw := []byte(perfGiftJSON)
	allocs := testing.AllocsPerRun(200, func() {
		_ = parseGift(raw)
	})
	logDanmakuMetric(t, "parse_gift_allocs", map[string]any{"allocs_per_op": fmt.Sprintf("%.1f", allocs)})
	if allocs > danmakuPerfMaxGiftParseAllocs {
		t.Errorf("parseGift allocs/op = %.1f, want <= %.1f", allocs, danmakuPerfMaxGiftParseAllocs)
	}
}

func TestEncode_AllocsPerOp(t *testing.T) {
	svc := newTestService()
	e, ok := parseDanmaku([]byte(perfDanmakuJSON))
	if !ok {
		t.Fatal("parse")
	}

	t.Run("xml", func(t *testing.T) {
		enc := xmlEncoder{}
		allocs := testing.AllocsPerRun(1000, func() {
			buf := svc.pool.GetBytes()
			_ = enc.AppendDanmaku(buf[:0], e, "1.234")
			svc.pool.PutBytes(buf)
		})
		logDanmakuMetric(t, "encode_xml_allocs", map[string]any{"allocs_per_op": fmt.Sprintf("%.1f", allocs)})
		if allocs > danmakuPerfMaxXMLEncodeAllocs {
			t.Errorf("xml encode allocs/op = %.1f, want <= %.1f", allocs, danmakuPerfMaxXMLEncodeAllocs)
		}
	})

	t.Run("jsonl", func(t *testing.T) {
		enc := jsonlEncoder{}
		allocs := testing.AllocsPerRun(1000, func() {
			buf := svc.pool.GetBytes()
			_ = enc.AppendDanmaku(buf[:0], e, "1.234")
			svc.pool.PutBytes(buf)
		})
		logDanmakuMetric(t, "encode_jsonl_allocs", map[string]any{"allocs_per_op": fmt.Sprintf("%.1f", allocs)})
		if allocs > danmakuPerfMaxJSONLEncodeAllocs {
			t.Errorf("jsonl encode allocs/op = %.1f, want <= %.1f", allocs, danmakuPerfMaxJSONLEncodeAllocs)
		}
	})
}

func TestHandleDanmaku_AllocsPerOp(t *testing.T) {
	quietDanmakuLogs(t)
	useDanmakuOverflowPolicy(t, "drop")

	svc := newTestService()
	sess, _ := newTestSession(svc, "jsonl")
	raw := []byte(perfDanmakuJSON)

	// Drain so AllocsPerRun does not include channel growth after capacity.
	allocs := testing.AllocsPerRun(200, func() {
		sess.handleDanmaku(raw)
		select {
		case frag := <-sess.msgCh:
			svc.pool.PutBytes(frag)
		default:
		}
	})
	logDanmakuMetric(t, "handle_danmaku_allocs", map[string]any{"allocs_per_op": fmt.Sprintf("%.1f", allocs)})
	if allocs > danmakuPerfMaxHandleAllocs {
		t.Errorf("handleDanmaku allocs/op = %.1f, want <= %.1f", allocs, danmakuPerfMaxHandleAllocs)
	}
}

func TestSession_HighRateWrite_NoLeak(t *testing.T) {
	quietDanmakuLogs(t)
	useDanmakuOverflowPolicy(t, "block")

	for _, format := range []string{"jsonl", "xml"} {
		t.Run(format, func(t *testing.T) {
			runHighRateWriteLeak(t, format, danmakuPerfHighRateMessages)
		})
	}
}

func runHighRateWriteLeak(t *testing.T, format string, n int) {
	t.Helper()

	svc := newTestService()
	svc.outputFormat = format
	sess, _ := newTestSession(svc, format)
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "rec.flv")
	outPath := PathForVideo(videoPath, sess.encoder.Ext())

	baselineAlloc, _, baselineG := snapshotHeap(true)
	startWriteLoop(t, sess, videoPath)

	raw := []byte(perfDanmakuJSON)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	startTotal := ms.TotalAlloc
	start := time.Now()
	for i := 0; i < n; i++ {
		sess.handleDanmaku(raw)
	}
	close(sess.msgCh)
	waitSessionDone(t, sess, 30*time.Second)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&ms)
	totalAllocMB := float64(ms.TotalAlloc-startTotal) / (1024 * 1024)

	afterAlloc, _, afterG := snapshotHeap(true)
	retained := afterAlloc - baselineAlloc
	growthG := afterG - baselineG
	msgsPerSec := float64(n) / elapsed.Seconds()
	dropped := sess.dropped.Load()
	written := sess.bytesWritten.Load()

	logDanmakuMetric(t, "high_rate_write", map[string]any{
		"format":            format,
		"msgs":              n,
		"duration_ns":       elapsed.Nanoseconds(),
		"msgs_per_s":        fmt.Sprintf("%.0f", msgsPerSec),
		"total_alloc_mb":    fmt.Sprintf("%.2f", totalAllocMB),
		"retained_alloc_mb": fmt.Sprintf("%.2f", retained),
		"goroutine_growth":  growthG,
		"dropped":           dropped,
		"bytes_written":     written,
	})

	if dropped != 0 {
		t.Errorf("dropped = %d, want 0 under block policy", dropped)
	}
	if msgsPerSec < danmakuPerfMinMessagesPerSec {
		t.Errorf("throughput = %.0f msg/s, want >= %d", msgsPerSec, danmakuPerfMinMessagesPerSec)
	}
	if retained > danmakuPerfMaxRetainedAllocMB {
		t.Errorf("possible leak: retained %.2f MB after GC (limit %.2f)", retained, danmakuPerfMaxRetainedAllocMB)
	}
	if growthG > danmakuPerfMaxGoroutineGrowth {
		t.Errorf("goroutines grew from %d to %d", baselineG, afterG)
	}
	if written == 0 {
		t.Fatal("bytes written = 0")
	}
	if format == "jsonl" {
		lines := countJSONLLines(t, outPath)
		// meta + n danmaku lines
		if lines != n+1 {
			t.Errorf("jsonl lines = %d, want %d", lines, n+1)
		}
	}

	buf := svc.pool.GetBytes()
	if cap(buf) != danmakuPerfPoolSize {
		t.Errorf("pool buffer cap = %d, want %d (grown buffers leaked into pool)", cap(buf), danmakuPerfPoolSize)
	}
	svc.pool.PutBytes(buf)
}

func TestSession_DropPolicy_BoundedMemory(t *testing.T) {
	quietDanmakuLogs(t)
	useDanmakuOverflowPolicy(t, "drop")

	svc := newTestService()
	sess, _ := newTestSession(svc, "jsonl")
	raw := []byte(perfDanmakuJSON)
	capacity := cap(sess.msgCh)

	baselineAlloc, _, _ := snapshotHeap(true)
	for i := 0; i < danmakuPerfFloodMessages; i++ {
		sess.handleDanmaku(raw)
	}
	dropped := sess.dropped.Load()
	queued := len(sess.msgCh)
	if queued != capacity {
		t.Errorf("queued = %d, want %d", queued, capacity)
	}
	wantDropped := uint64(danmakuPerfFloodMessages - capacity)
	if dropped != wantDropped {
		t.Errorf("dropped = %d, want %d", dropped, wantDropped)
	}

	for len(sess.msgCh) > 0 {
		svc.pool.PutBytes(<-sess.msgCh)
	}
	afterAlloc, _, _ := snapshotHeap(true)
	retained := afterAlloc - baselineAlloc

	logDanmakuMetric(t, "drop_policy_bounded_memory", map[string]any{
		"flood_msgs":        danmakuPerfFloodMessages,
		"queued":            queued,
		"dropped":           dropped,
		"retained_alloc_mb": fmt.Sprintf("%.2f", retained),
	})
	if retained > danmakuPerfMaxFloodRetainedMB {
		t.Errorf("drop policy retained %.2f MB after GC (limit %.2f); queued work should stay bounded by chan capacity",
			retained, danmakuPerfMaxFloodRetainedMB)
	}
}

func TestSession_RotateUnderLoad_NoLeak(t *testing.T) {
	quietDanmakuLogs(t)
	useDanmakuOverflowPolicy(t, "block")

	svc := newTestService()
	sess, _ := newTestSession(svc, "jsonl")
	dir := t.TempDir()
	raw := []byte(perfDanmakuJSON)

	baselineAlloc, _, baselineG := snapshotHeap(true)
	startWriteLoop(t, sess, filepath.Join(dir, "rec-0.flv"))

	totalMsgs := 0
	for round := 1; round <= danmakuPerfRotateRounds; round++ {
		for i := 0; i < danmakuPerfRotateBatch; i++ {
			sess.handleDanmaku(raw)
			totalMsgs++
		}
		req := rotateRequest{
			videoPath:    filepath.Join(dir, fmt.Sprintf("rec-%d.flv", round)),
			segmentStart: time.Now(),
		}
		select {
		case sess.rotateCh <- req:
		case <-time.After(5 * time.Second):
			t.Fatalf("rotate %d blocked", round)
		}
		waitForRotation(t, sess, req.segmentStart, 5*time.Second)
	}
	for i := 0; i < danmakuPerfRotateBatch; i++ {
		sess.handleDanmaku(raw)
		totalMsgs++
	}
	close(sess.msgCh)
	waitSessionDone(t, sess, 15*time.Second)

	afterAlloc, _, afterG := snapshotHeap(true)
	retained := afterAlloc - baselineAlloc
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	logDanmakuMetric(t, "rotate_under_load", map[string]any{
		"rounds":            danmakuPerfRotateRounds,
		"msgs":              totalMsgs,
		"sidecar_files":     len(files),
		"retained_alloc_mb": fmt.Sprintf("%.2f", retained),
		"goroutine_growth":  afterG - baselineG,
		"dropped":           sess.dropped.Load(),
	})

	wantFiles := danmakuPerfRotateRounds + 1
	if len(files) != wantFiles {
		t.Errorf("sidecar files = %d, want %d", len(files), wantFiles)
	}
	if sess.dropped.Load() != 0 {
		t.Errorf("dropped = %d, want 0", sess.dropped.Load())
	}
	if retained > danmakuPerfMaxRetainedAllocMB {
		t.Errorf("possible leak after rotations: retained %.2f MB", retained)
	}
	if afterG-baselineG > danmakuPerfMaxGoroutineGrowth {
		t.Errorf("goroutines grew from %d to %d", baselineG, afterG)
	}
}

func TestSession_ConcurrentWriteLoops_NoLeak(t *testing.T) {
	quietDanmakuLogs(t)
	useDanmakuOverflowPolicy(t, "block")

	svc := newTestService()
	dir := t.TempDir()
	raw := []byte(perfDanmakuJSON)

	baselineAlloc, _, baselineG := snapshotHeap(true)

	var wg sync.WaitGroup
	sessions := make([]*session, danmakuPerfConcurrentSessions)
	for i := 0; i < danmakuPerfConcurrentSessions; i++ {
		enc, err := NewFormatEncoder("jsonl")
		if err != nil {
			t.Fatal(err)
		}
		meta := RoomMeta{RoomID: int64(1000 + i), Uname: "u", Title: "t"}
		sess := newSession(1000+i, meta, svc, enc, context.Background())
		sessions[i] = sess
		videoPath := filepath.Join(dir, fmt.Sprintf("room-%d.flv", i))
		startWriteLoop(t, sess, videoPath)

		wg.Add(1)
		go func(s *session) {
			defer wg.Done()
			for j := 0; j < danmakuPerfConcurrentMessages; j++ {
				s.handleDanmaku(raw)
			}
			close(s.msgCh)
		}(sess)
	}
	wg.Wait()
	for _, sess := range sessions {
		waitSessionDone(t, sess, 20*time.Second)
		if sess.dropped.Load() != 0 {
			t.Errorf("room %d dropped = %d, want 0", sess.roomID, sess.dropped.Load())
		}
	}

	afterAlloc, _, afterG := snapshotHeap(true)
	retained := afterAlloc - baselineAlloc
	logDanmakuMetric(t, "concurrent_write_loops", map[string]any{
		"sessions":          danmakuPerfConcurrentSessions,
		"msgs_per_session":  danmakuPerfConcurrentMessages,
		"retained_alloc_mb": fmt.Sprintf("%.2f", retained),
		"goroutine_growth":  afterG - baselineG,
	})
	if retained > danmakuPerfMaxRetainedAllocMB+float64(danmakuPerfConcurrentSessions) {
		t.Errorf("possible leak in concurrent writers: retained %.2f MB", retained)
	}
	if afterG-baselineG > danmakuPerfMaxGoroutineGrowth {
		t.Errorf("goroutines grew from %d to %d", baselineG, afterG)
	}
}

func TestService_StartSessionCycles_NoLeak(t *testing.T) {
	quietDanmakuLogs(t)
	useDanmakuOverflowPolicy(t, "block")

	svc := newTestService()
	dir := t.TempDir()
	raw := []byte(perfDanmakuJSON)
	meta := RoomMeta{RoomID: 1, Uname: "u", Title: "t"}

	baselineAlloc, _, baselineG := snapshotHeap(true)
	for i := 0; i < danmakuPerfStartStopCycles; i++ {
		enc, err := NewFormatEncoder("jsonl")
		if err != nil {
			t.Fatal(err)
		}
		sess := newSession(1, meta, svc, enc, context.Background())
		svc.sessions.Store(1, sess)
		videoPath := filepath.Join(dir, fmt.Sprintf("rec-%d.flv", i))
		startWriteLoop(t, sess, videoPath)
		sess.handleDanmaku(raw)
		close(sess.msgCh)
		waitSessionDone(t, sess, 5*time.Second)
		waitActiveSessions(t, svc, 0, 2*time.Second)
	}

	afterAlloc, _, afterG := snapshotHeap(true)
	retained := afterAlloc - baselineAlloc
	logDanmakuMetric(t, "start_session_cycles", map[string]any{
		"cycles":            danmakuPerfStartStopCycles,
		"retained_alloc_mb": fmt.Sprintf("%.2f", retained),
		"goroutine_growth":  afterG - baselineG,
		"active_sessions":   svc.ActiveSessions(),
	})
	if svc.ActiveSessions() != 0 {
		t.Errorf("active sessions = %d, want 0", svc.ActiveSessions())
	}
	if retained > danmakuPerfMaxRetainedAllocMB {
		t.Errorf("possible leak after StartSession cycles: retained %.2f MB", retained)
	}
	if afterG-baselineG > danmakuPerfMaxGoroutineGrowth {
		t.Errorf("goroutines grew from %d to %d after %d cycles", baselineG, afterG, danmakuPerfStartStopCycles)
	}
}
