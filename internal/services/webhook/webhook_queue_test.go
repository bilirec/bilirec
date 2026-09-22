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

func TestEnqueueDoesNotBlockWhenQueueFull(t *testing.T) {
	block := make(chan struct{})
	var received int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received++
		mu.Unlock()
		<-block
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{WebhookURLs: srv.URL}
	svc := newWebhookService(t, cfg)
	room := &bilibili.LiveRoomInfoDetail{RoomID: 42, Uname: "u", Title: "t"}

	done := make(chan struct{})
	go func() {
		for i := 0; i < perRoomQueueCap+10; i++ {
			svc.StreamStarted(room)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit blocked while room queue was full")
	}
	close(block)

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := received
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected at least one delivery, got %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if received > perRoomQueueCap+1 {
		t.Fatalf("expected at most %d deliveries, got %d", perRoomQueueCap+1, received)
	}
}

func TestEnqueueDoesNotBlockWhenPendingFull(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{WebhookURLs: srv.URL}
	svc := newWebhookService(t, cfg)

	room1 := &bilibili.LiveRoomInfoDetail{RoomID: 1, Uname: "u", Title: "t"}
	svc.StreamStarted(room1)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(svc.pending) >= pendingWorkCap {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending did not fill, len=%d cap=%d", len(svc.pending), pendingWorkCap)
		}
		for i := 2; i <= pendingWorkCap+2 && len(svc.pending) < pendingWorkCap; i++ {
			svc.StreamStarted(&bilibili.LiveRoomInfoDetail{RoomID: int64(i), Uname: "u", Title: "t"})
		}
		time.Sleep(5 * time.Millisecond)
	}

	done := make(chan struct{})
	go func() {
		for i := pendingWorkCap + 10; i <= pendingWorkCap + 30; i++ {
			svc.StreamStarted(&bilibili.LiveRoomInfoDetail{RoomID: int64(i), Uname: "u", Title: "t"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit blocked while pending was full")
	}
	close(block)
}

func TestSameRoomEventsDeliveredInOrder(t *testing.T) {
	var mu sync.Mutex
	var order []EventType
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var env envelope
		_ = json.Unmarshal(b, &env)
		mu.Lock()
		order = append(order, env.EventType)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "room", "a.flv")
	if err := os.MkdirAll(filepath.Dir(videoPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(videoPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{OutputDir: dir, WebhookURLs: srv.URL}
	svc := newWebhookService(t, cfg)
	room := &bilibili.LiveRoomInfoDetail{
		RoomID: 99,
		Uname:  "u",
		Title:  "t",
	}
	open := time.Now().Add(-time.Second)
	closeAt := time.Now()

	svc.FileOpening(room, "sid", videoPath, open, true, true)
	svc.FileClosed(room, "sid", videoPath, open, closeAt, true, true)

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 2 events, got %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 {
		t.Fatalf("expected 2 events, got %d", len(order))
	}
	if order[0] != EventFileOpening || order[1] != EventFileClosed {
		t.Fatalf("order %v", order)
	}
}
