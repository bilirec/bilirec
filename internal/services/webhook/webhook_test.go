package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
)

func TestFileClosedPayloadAndRetry(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "room", "clip.flv")
	if err := os.MkdirAll(filepath.Dir(videoPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(videoPath, []byte("testdata"), 0644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	attempts := 0
	var bodies []envelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()

		b, _ := io.ReadAll(r.Body)
		var env envelope
		if err := json.Unmarshal(b, &env); err != nil {
			t.Errorf("invalid json: %v", err)
		} else {
			mu.Lock()
			bodies = append(bodies, env)
			mu.Unlock()
		}

		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		OutputDir:   dir,
		WebhookURLs: srv.URL,
	}
	svc := newWebhookService(t, cfg)
	room := &bilibili.LiveRoomInfoDetail{
		RoomID:         23058,
		ShortID:        3,
		Uname:          "主播",
		Title:          "标题",
		ParentAreaName: "生活",
		AreaName:       "影音馆",
		LiveStatus:     1,
	}
	open := time.Now().Add(-2 * time.Second)
	closeAt := time.Now()
	svc.FileClosed(room, "session-1", videoPath, open, closeAt, true, true)
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := attempts
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected at least 2 attempts, got %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", attempts)
	}
	if len(bodies) == 0 {
		t.Fatal("no webhook body captured")
	}
	last := bodies[len(bodies)-1]
	if last.EventType != EventFileClosed {
		t.Fatalf("event type %q", last.EventType)
	}
	if last.EventID == "" {
		t.Fatal("missing EventId")
	}
	data, err := json.Marshal(last.EventData)
	if err != nil {
		t.Fatal(err)
	}
	var parsed roomEventData
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.RelativePath != "room/clip.flv" {
		t.Fatalf("relative path %q", parsed.RelativePath)
	}
	if parsed.FileSize != 8 {
		t.Fatalf("file size %d", parsed.FileSize)
	}
}
