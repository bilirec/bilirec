package webhook_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/testutil/recording"
)

// BililiveRecorder Webhook v2 JSON (integration assertions).
type integrationWebhookEnvelope struct {
	EventType      string          `json:"EventType"`
	EventTimestamp string          `json:"EventTimestamp"`
	EventID        string          `json:"EventId"`
	EventData      json.RawMessage `json:"EventData"`
}

type integrationWebhookEventData struct {
	SessionID        string  `json:"SessionId"`
	RoomID           int     `json:"RoomId"`
	ShortID          int64   `json:"ShortId"`
	Name             string  `json:"Name"`
	Title            string  `json:"Title"`
	AreaNameParent   string  `json:"AreaNameParent"`
	AreaNameChild    string  `json:"AreaNameChild"`
	Recording        bool    `json:"Recording"`
	Streaming        bool    `json:"Streaming"`
	DanmakuConnected bool    `json:"DanmakuConnected"`
	RelativePath     string  `json:"RelativePath"`
	FileOpenTime     string  `json:"FileOpenTime"`
	FileCloseTime    string  `json:"FileCloseTime"`
	FileSize         int64   `json:"FileSize"`
	Duration         float64 `json:"Duration"`
}

type webhookIntegrationCollector struct {
	mu    sync.Mutex
	posts []integrationWebhookEnvelope
}

func (c *webhookIntegrationCollector) snapshot() []integrationWebhookEnvelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]integrationWebhookEnvelope, len(c.posts))
	copy(out, c.posts)
	return out
}

func startWebhookIntegrationCollector(t *testing.T) (baseURL string, collector *webhookIntegrationCollector) {
	t.Helper()

	collector = &webhookIntegrationCollector{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen webhook collector: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var env integrationWebhookEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		collector.mu.Lock()
		collector.posts = append(collector.posts, env)
		collector.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			t.Logf("webhook collector stopped: %v", serveErr)
		}
	}()
	t.Cleanup(func() { _ = srv.Close() })

	return "http://" + ln.Addr().String(), collector
}

func waitForWebhookEventTypes(
	t *testing.T,
	collector *webhookIntegrationCollector,
	roomID int,
	required []string,
	timeout time.Duration,
) []integrationWebhookEnvelope {
	t.Helper()

	deadline := time.Now().Add(timeout)
	want := make(map[string]bool, len(required))
	for _, et := range required {
		want[et] = true
	}

	for {
		posts := filterWebhookPostsForRoom(collector.snapshot(), roomID)
		for _, p := range posts {
			delete(want, p.EventType)
		}
		if len(want) == 0 {
			return posts
		}
		if time.Now().After(deadline) {
			missing := make([]string, 0, len(want))
			for et := range want {
				missing = append(missing, et)
			}
			sort.Strings(missing)
			got := eventTypeCounts(posts)
			t.Fatalf("webhook drain timeout: missing %v for room %d; got %v", missing, roomID, got)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func filterWebhookPostsForRoom(posts []integrationWebhookEnvelope, roomID int) []integrationWebhookEnvelope {
	var out []integrationWebhookEnvelope
	for _, p := range posts {
		data, ok := parseIntegrationWebhookData(p.EventData)
		if !ok || data.RoomID != roomID {
			continue
		}
		out = append(out, p)
	}
	return out
}

func parseIntegrationWebhookData(raw json.RawMessage) (integrationWebhookEventData, bool) {
	if len(raw) == 0 {
		return integrationWebhookEventData{}, false
	}
	var data integrationWebhookEventData
	if err := json.Unmarshal(raw, &data); err != nil {
		return integrationWebhookEventData{}, false
	}
	return data, true
}

func eventTypeCounts(posts []integrationWebhookEnvelope) map[string]int {
	counts := make(map[string]int)
	for _, p := range posts {
		counts[p.EventType]++
	}
	return counts
}

func assertWebhookRecordingLifecycle(
	t *testing.T,
	posts []integrationWebhookEnvelope,
	roomID int,
	outputDir string,
	outputPath string,
	room *bilibili.LiveRoomInfoDetail,
) {
	t.Helper()

	byType := make(map[string][]integrationWebhookEnvelope)
	for _, p := range posts {
		byType[p.EventType] = append(byType[p.EventType], p)
	}

	sessionStarted := byType["SessionStarted"]
	if len(sessionStarted) != 1 {
		t.Fatalf("SessionStarted count %d", len(sessionStarted))
	}
	fileOpening := byType["FileOpening"]
	if len(fileOpening) != 1 {
		t.Fatalf("FileOpening count %d", len(fileOpening))
	}
	fileClosed := byType["FileClosed"]
	if len(fileClosed) != 1 {
		t.Fatalf("FileClosed count %d", len(fileClosed))
	}
	sessionEnded := byType["SessionEnded"]
	if len(sessionEnded) != 1 {
		t.Fatalf("SessionEnded count %d", len(sessionEnded))
	}

	sessionData, ok := parseIntegrationWebhookData(sessionStarted[0].EventData)
	if !ok || sessionData.SessionID == "" {
		t.Fatal("SessionStarted missing SessionId")
	}
	if sessionData.RoomID != roomID {
		t.Fatalf("SessionStarted RoomId %d", sessionData.RoomID)
	}
	if !sessionData.Recording {
		t.Fatal("SessionStarted expected Recording=true")
	}

	openData, ok := parseIntegrationWebhookData(fileOpening[0].EventData)
	if !ok || openData.SessionID != sessionData.SessionID {
		t.Fatalf("FileOpening SessionId mismatch: %q", openData.SessionID)
	}
	if openData.RelativePath == "" {
		t.Fatal("FileOpening missing RelativePath")
	}

	closeData, ok := parseIntegrationWebhookData(fileClosed[0].EventData)
	if !ok || closeData.SessionID != sessionData.SessionID {
		t.Fatalf("FileClosed SessionId mismatch: %q", closeData.SessionID)
	}
	if closeData.RelativePath != openData.RelativePath {
		t.Fatalf("FileClosed RelativePath %q != FileOpening %q", closeData.RelativePath, openData.RelativePath)
	}
	if closeData.FileCloseTime == "" || closeData.FileOpenTime == "" {
		t.Fatal("FileClosed missing file times")
	}
	if closeData.FileSize <= 0 {
		t.Fatalf("FileClosed FileSize %d", closeData.FileSize)
	}
	if closeData.Duration < 0 {
		t.Fatalf("FileClosed Duration %v", closeData.Duration)
	}

	endData, ok := parseIntegrationWebhookData(sessionEnded[0].EventData)
	if !ok || endData.SessionID != sessionData.SessionID {
		t.Fatalf("SessionEnded SessionId mismatch: %q", endData.SessionID)
	}
	if endData.Recording {
		t.Fatal("SessionEnded expected Recording=false")
	}

	wantRel, err := filepath.Rel(outputDir, outputPath)
	if err != nil {
		t.Fatalf("rel output path: %v", err)
	}
	wantRel = filepath.ToSlash(wantRel)
	if closeData.RelativePath != wantRel {
		t.Fatalf("FileClosed RelativePath %q want %q", closeData.RelativePath, wantRel)
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if closeData.FileSize != stat.Size() {
		t.Fatalf("FileClosed FileSize %d != file %d", closeData.FileSize, stat.Size())
	}

	if room != nil {
		if sessionData.Name != room.Uname {
			t.Fatalf("Name %q want %q", sessionData.Name, room.Uname)
		}
		if sessionData.Title != room.Title {
			t.Fatalf("Title %q want %q", sessionData.Title, room.Title)
		}
		if int64(roomID) != room.RoomID {
			t.Fatalf("room id mismatch")
		}
		if sessionData.ShortID != room.ShortID {
			t.Fatalf("ShortId %d want %d", sessionData.ShortID, room.ShortID)
		}
	}

	for _, p := range posts {
		if p.EventID == "" {
			t.Fatalf("%s missing EventId", p.EventType)
		}
		if strings.TrimSpace(p.EventTimestamp) == "" {
			t.Fatalf("%s missing EventTimestamp", p.EventType)
		}
	}

	idx := func(et string) int {
		for i, p := range posts {
			if p.EventType == et {
				return i
			}
		}
		return -1
	}
	// bilirec: SessionEnded is emitted from Stop() with metrics; FileClosed follows async finalize.
	if idx("SessionStarted") < 0 || idx("FileOpening") < 0 || idx("FileClosed") < 0 || idx("SessionEnded") < 0 {
		t.Fatal("missing required webhook events in delivery order")
	}
	if idx("FileOpening") <= idx("SessionStarted") {
		t.Fatalf("FileOpening at %d must follow SessionStarted at %d", idx("FileOpening"), idx("SessionStarted"))
	}
	if idx("FileClosed") <= idx("FileOpening") {
		t.Fatalf("FileClosed at %d must follow FileOpening at %d", idx("FileClosed"), idx("FileOpening"))
	}
	if idx("SessionEnded") <= idx("SessionStarted") {
		t.Fatalf("SessionEnded at %d must follow SessionStarted at %d", idx("SessionEnded"), idx("SessionStarted"))
	}
}

func runWebhookIntegrationRecordTest(t *testing.T) {
	t.Helper()

	outputDir := t.TempDir()
	webhookURL, collector := startWebhookIntegrationCollector(t)
	t.Setenv("OUTPUT_DIR", outputDir)
	t.Setenv("WEBHOOK_URLS", webhookURL)

	sess := recording.NewSession(t)
	roomID := recording.ResolveLiveTestRoomID(t, sess.Room)
	roomInfo, err := sess.Room.GetLiveRoomInfo(roomID)
	if err != nil {
		t.Fatalf("GetLiveRoomInfo: %v", err)
	}

	startErr := sess.Recorder.Start(roomID)
	recording.HandleRecordingStartErr(t, startErr)

	outputPath := recording.WaitForOutputPathAfterStart(t, sess.Recorder, roomID)
	recordDuration := recording.IntegrationRecordDuration()
	t.Logf("webhook integration: recording room=%d for %s (webhook=%s)", roomID, recordDuration, webhookURL)
	_ = sess.Monitor.RunRecordingProfiledWait(t, "webhook_recording", recordDuration)

	t.Log("stopping recording")
	if !sess.Recorder.Stop(roomID) {
		t.Fatal("stop returned false")
	}
	recording.WaitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	time.Sleep(recording.SettleAfterStop)

	drainTimeout := 3 * time.Minute
	if os.Getenv("CI") != "" {
		drainTimeout = 5 * time.Minute
	}
	required := []string{"SessionStarted", "FileOpening", "FileClosed", "SessionEnded"}
	posts := waitForWebhookEventTypes(t, collector, roomID, required, drainTimeout)
	assertWebhookRecordingLifecycle(t, posts, roomID, outputDir, outputPath, roomInfo)

	t.Logf("webhook integration ok: %d events for room %d", len(posts), roomID)
}

// Long-running live recording with WEBHOOK_URLS pointed at a local collector.
// Isolated run (CI runs separately from default recorder integration):
//
//	go test ./internal/services/webhook -run TestLong_WebhookDuringRecord -count=1 -timeout 30m
func TestLong_WebhookDuringRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping webhook integration record test in short mode")
	}
	runWebhookIntegrationRecordTest(t)
}
