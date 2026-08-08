package recorder_test

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/services/danmaku"
)

// danmakuXMLStats summarizes a parsed danmaku XML file for assertions.
type danmakuXMLStats struct {
	HasRoot       bool
	HasRecordInfo bool
	DanmakuCount  int
	SCCount       int
	GiftCount     int
	GuardCount    int
}

type danmakuJSONLStats struct {
	HasMeta      bool
	DanmakuCount int
	SCCount      int
	GiftCount    int
	GuardCount   int
}

// parseDanmakuXML validates well-formedness and counts message elements.
// Every <d> p attribute must have 8 comma-separated fields with a
// non-negative relative timestamp as the first field.
func parseDanmakuXML(t *testing.T, path string) danmakuXMLStats {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open danmaku xml %s: %v", path, err)
	}
	defer f.Close()

	var stats danmakuXMLStats
	decoder := xml.NewDecoder(f)
	for {
		tok, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("danmaku xml %s not well-formed: %v", path, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "i":
			stats.HasRoot = true
		case "BilirecRecordInfo":
			stats.HasRecordInfo = true
		case "d":
			stats.DanmakuCount++
			assertDanmakuPAttr(t, path, start)
		case "sc":
			stats.SCCount++
		case "gift":
			stats.GiftCount++
		case "guard":
			stats.GuardCount++
		}
	}
	return stats
}

func parseDanmakuJSONL(t *testing.T, path string) danmakuJSONLStats {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open danmaku jsonl %s: %v", path, err)
	}
	defer f.Close()

	var stats danmakuJSONLStats
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("danmaku jsonl %s invalid line: %v\n%s", path, err, line)
		}
		switch m["type"] {
		case "meta":
			stats.HasMeta = true
		case "danmaku":
			stats.DanmakuCount++
			if ts, ok := m["ts"].(float64); !ok || ts < 0 {
				t.Errorf("%s: danmaku ts invalid: %v", path, m["ts"])
			}
		case "super_chat":
			stats.SCCount++
		case "gift":
			stats.GiftCount++
		case "guard":
			stats.GuardCount++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan danmaku jsonl %s: %v", path, err)
	}
	return stats
}

func assertDanmakuPAttr(t *testing.T, path string, start xml.StartElement) {
	t.Helper()
	for _, attr := range start.Attr {
		if attr.Name.Local != "p" {
			continue
		}
		fields := strings.Split(attr.Value, ",")
		if len(fields) != 8 {
			t.Errorf("%s: <d> p attribute has %d fields, want 8: %q", path, len(fields), attr.Value)
			return
		}
		ts, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			t.Errorf("%s: <d> ts not parseable: %q", path, fields[0])
			return
		}
		if ts < 0 {
			t.Errorf("%s: <d> ts negative: %f", path, ts)
		}
		return
	}
	t.Errorf("%s: <d> missing p attribute", path)
}

func waitForFinalizedDanmakuXML(t *testing.T, xmlPath string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(xmlPath); err == nil && strings.Contains(string(data), "</i>") {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func waitForFinalizedDanmakuJSONL(t *testing.T, jsonlPath string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(jsonlPath); err == nil && strings.Contains(string(data), `"type":"meta"`) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func videoFormatFromPath(t *testing.T, videoPath string) string {
	t.Helper()
	switch strings.ToLower(filepath.Ext(videoPath)) {
	case ".flv":
		return "flv"
	case ".ts":
		return "ts"
	case ".fmp4", ".mp4":
		return "fmp4"
	default:
		t.Skipf("unknown video extension for ffprobe verification: %s", videoPath)
		return ""
	}
}

func TestDanmakuJsonlRecord(t *testing.T) {
	runDanmakuRecordTest(t, "jsonl")
}

func TestDanmakuXmlRecord(t *testing.T) {
	runDanmakuRecordTest(t, "xml")
}

func runDanmakuRecordTest(t *testing.T, format string) {
	t.Helper()
	if testing.Short() {
		t.Skipf("skipping danmaku %s record test in short mode", format)
	}

	t.Setenv("DANMAKU_RECORD_ENABLED", "true")
	t.Setenv("DANMAKU_OUTPUT_FORMAT", format)

	sess := newRecorderTestSession(t)
	// Pin a high-traffic room via BILIBILI_TEST_ROOM_ID when needed (e.g. 1947277414).
	roomID := resolveLiveTestRoomID(t, sess.Room)

	startErr := sess.Recorder.Start(roomID)
	handleRecordingStartErr(t, startErr)

	outputPath := waitForOutputPathAfterStart(t, sess.Recorder, roomID)
	sidecarPath := danmaku.PathForVideo(outputPath, "."+format)

	const recordDuration = 75 * time.Second
	t.Logf("recording room=%d for %s with danmaku format=%s; video=%s", roomID, recordDuration, format, outputPath)
	time.Sleep(recordDuration)

	if !sess.Recorder.Stop(roomID) {
		t.Error("failed to stop recording")
	}
	waitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)

	switch format {
	case "jsonl":
		if !waitForFinalizedDanmakuJSONL(t, sidecarPath, 20*time.Second) {
			t.Fatalf("danmaku jsonl not finalized within timeout: %s", sidecarPath)
		}
		stats := parseDanmakuJSONL(t, sidecarPath)
		t.Logf("danmaku jsonl stats: d=%d sc=%d gift=%d guard=%d", stats.DanmakuCount, stats.SCCount, stats.GiftCount, stats.GuardCount)
		if !stats.HasMeta {
			t.Error("danmaku jsonl missing meta line")
		}
		if stats.DanmakuCount == 0 {
			if live, err := sess.Room.IsRoomLive(roomID); err != nil || !live {
				t.Skipf("room %d went offline during test and no danmaku was recorded", roomID)
			}
			t.Skipf("no danmaku lines recorded in %s on a live room; treating as quiet-room window", recordDuration)
		}
	case "xml":
		if !waitForFinalizedDanmakuXML(t, sidecarPath, 20*time.Second) {
			t.Fatalf("danmaku xml not finalized within timeout: %s", sidecarPath)
		}
		stats := parseDanmakuXML(t, sidecarPath)
		t.Logf("danmaku xml stats: d=%d sc=%d gift=%d guard=%d", stats.DanmakuCount, stats.SCCount, stats.GiftCount, stats.GuardCount)
		if !stats.HasRoot {
			t.Error("danmaku xml missing <i> root element")
		}
		if !stats.HasRecordInfo {
			t.Error("danmaku xml missing BilirecRecordInfo header element")
		}
		if stats.DanmakuCount == 0 {
			if live, err := sess.Room.IsRoomLive(roomID); err != nil || !live {
				t.Skipf("room %d went offline during test and no danmaku was recorded", roomID)
			}
			t.Skipf("no <d> elements recorded in %s on a live room; treating as quiet-room window", recordDuration)
		}
	default:
		t.Fatalf("unsupported danmaku format %q", format)
	}

	if checkFFmpegAvailable(t) {
		verifyAllRecordingsInRoomDir(t, filepath.Dir(outputPath), videoFormatFromPath(t, outputPath))
	}
}

func TestDanmakuRecord_Disabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping danmaku disabled test in short mode")
	}
	if os.Getenv("DANMAKU_RECORD_ENABLED") == "true" {
		t.Skip("DANMAKU_RECORD_ENABLED is set in the environment; disabled-case test requires it unset/false")
	}

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	time.Sleep(time.Second)
	baselineGoroutines := sess.Monitor.snapshotGoroutines(t, "danmaku_disabled_baseline")

	startErr := sess.Recorder.Start(roomID)
	handleRecordingStartErr(t, startErr)

	outputPath := waitForOutputPathAfterStart(t, sess.Recorder, roomID)
	roomDir := filepath.Dir(outputPath)
	existingSidecars := make(map[string]struct{})
	for _, pattern := range []string{"*.xml", "*.jsonl"} {
		before, err := filepath.Glob(filepath.Join(roomDir, pattern))
		if err != nil {
			t.Fatalf("glob existing %s files: %v", pattern, err)
		}
		for _, path := range before {
			existingSidecars[path] = struct{}{}
		}
	}

	const recordDuration = 20 * time.Second
	t.Logf("recording room=%d for %s with danmaku disabled", roomID, recordDuration)
	time.Sleep(recordDuration / 2)

	if n := sess.Danmaku.ActiveSessions(); n != 0 {
		t.Errorf("danmaku disabled but %d active session(s) during recording", n)
	}
	time.Sleep(recordDuration / 2)

	if !sess.Recorder.Stop(roomID) {
		t.Error("failed to stop recording")
	}
	waitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	time.Sleep(recorderTestSettleAfterStop)

	if n := sess.Danmaku.ActiveSessions(); n != 0 {
		t.Errorf("danmaku disabled but %d active session(s) after stop", n)
	}

	var newSidecars []string
	for _, pattern := range []string{"*.xml", "*.jsonl"} {
		files, err := filepath.Glob(filepath.Join(roomDir, pattern))
		if err != nil {
			t.Fatalf("glob %s files: %v", pattern, err)
		}
		for _, path := range files {
			if _, existed := existingSidecars[path]; !existed {
				newSidecars = append(newSidecars, path)
			}
		}
	}
	if len(newSidecars) != 0 {
		t.Errorf("danmaku disabled but new sidecar files exist: %v", newSidecars)
	}

	afterGoroutines := sess.Monitor.snapshotGoroutines(t, "danmaku_disabled_after_stop")
	if growth := afterGoroutines - baselineGoroutines; growth > 10 {
		t.Errorf("goroutines grew from %d to %d with danmaku disabled; possible leak", baselineGoroutines, afterGoroutines)
	}
}
