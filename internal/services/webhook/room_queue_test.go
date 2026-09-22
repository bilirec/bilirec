package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
)

func testEnvelope(roomID int, eventType EventType) envelope {
	return envelope{
		EventType:      eventType,
		EventTimestamp: time.Now().Format(time.RFC3339Nano),
		EventID:        "test-event-id",
		EventData:      roomEventData{RoomID: roomID, Name: "u", Title: "t"},
	}
}

func TestTrySendClosedReturnsRetry(t *testing.T) {
	q := newRoomQueue()
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()

	queued, retry := q.trySend(1, testEnvelope(1, EventStreamStarted))
	if queued || !retry {
		t.Fatalf("queued=%v retry=%v", queued, retry)
	}
}

func TestTrySendDropWhenChannelFull(t *testing.T) {
	q := newRoomQueue()
	body := testEnvelope(2, EventStreamStarted)
	for i := 0; i < perRoomQueueCap; i++ {
		queued, retry := q.trySend(2, body)
		if !queued || retry {
			t.Fatalf("fill %d: queued=%v retry=%v", i, queued, retry)
		}
	}
	queued, retry := q.trySend(2, body)
	if queued || retry {
		t.Fatalf("overflow: queued=%v retry=%v", queued, retry)
	}
	if len(q.ch) != perRoomQueueCap {
		t.Fatalf("channel len %d", len(q.ch))
	}
}

func TestEvictIdleRoomDeletesFromMap(t *testing.T) {
	cfg := &config.Config{WebhookURLs: "http://127.0.0.1:9"}
	svc := newWebhookService(t, cfg)

	const roomID = 88
	q := svc.getOrCreateQueue(roomID)
	q.mu.Lock()
	q.emptySince = time.Now().Add(-roomIdleEvict - time.Second)
	q.mu.Unlock()

	svc.evictIdleRooms()

	if _, ok := svc.rooms.Load(roomID); ok {
		t.Fatal("evicted room still in map")
	}
	if !q.closed {
		t.Fatal("queue not marked closed")
	}
}

func TestEnqueueSucceedsAfterEvictedQueueRemovedFromMap(t *testing.T) {
	var mu sync.Mutex
	var bodies []envelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var env envelope
		_ = json.Unmarshal(b, &env)
		mu.Lock()
		bodies = append(bodies, env)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{WebhookURLs: srv.URL}
	svc := newWebhookService(t, cfg)

	const roomID = 77
	old := svc.getOrCreateQueue(roomID)
	old.mu.Lock()
	old.closed = true
	old.mu.Unlock()
	if !svc.compareAndDeleteRoom(roomID, old) {
		t.Fatal("compareAndDeleteRoom failed")
	}

	svc.enqueue(roomID, testEnvelope(roomID, EventStreamStarted))

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(bodies)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("event not delivered after queue replaced")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEnqueueDropsWhenClosedQueueStillInMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{WebhookURLs: srv.URL}
	svc := newWebhookService(t, cfg)

	const roomID = 66
	q := svc.getOrCreateQueue(roomID)
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()

	svc.enqueue(roomID, testEnvelope(roomID, EventStreamStarted))
	time.Sleep(100 * time.Millisecond)
}

func TestRescheduleBackloggedRoomsDelivers(t *testing.T) {
	var mu sync.Mutex
	delivered := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		delivered = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{WebhookURLs: srv.URL}
	svc := newWebhookService(t, cfg)

	const roomID = 55
	q := svc.getOrCreateQueue(roomID)
	queued, retry := q.trySend(roomID, testEnvelope(roomID, EventStreamStarted))
	if !queued || retry {
		t.Fatalf("trySend queued=%v retry=%v", queued, retry)
	}
	if q.scheduled.Load() {
		t.Fatal("expected not scheduled")
	}

	svc.rescheduleBackloggedRooms()

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		ok := delivered
		mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("reschedule did not lead to delivery")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPendingFullBacklogDeliveredAfterWorkerDrains(t *testing.T) {
	block := make(chan struct{})
	var mu sync.Mutex
	var targetSeen bool
	const targetRoom = pendingWorkCap + 50

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var env envelope
		_ = json.Unmarshal(b, &env)
		data, _ := json.Marshal(env.EventData)
		var parsed roomEventData
		_ = json.Unmarshal(data, &parsed)
		if parsed.RoomID == targetRoom {
			mu.Lock()
			targetSeen = true
			mu.Unlock()
		}
		<-block
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{WebhookURLs: srv.URL}
	svc := newWebhookService(t, cfg)

	svc.StreamStarted(&bilibili.LiveRoomInfoDetail{RoomID: 1, Uname: "u", Title: "t"})

	fillDeadline := time.Now().Add(2 * time.Second)
	for len(svc.pending) < pendingWorkCap {
		if time.Now().After(fillDeadline) {
			t.Fatalf("pending len=%d", len(svc.pending))
		}
		for i := 2; i <= pendingWorkCap+2 && len(svc.pending) < pendingWorkCap; i++ {
			svc.StreamStarted(&bilibili.LiveRoomInfoDetail{RoomID: int64(i), Uname: "u", Title: "t"})
		}
		time.Sleep(5 * time.Millisecond)
	}

	svc.StreamStarted(&bilibili.LiveRoomInfoDetail{RoomID: int64(targetRoom), Uname: "u", Title: "t"})

	q, ok := svc.rooms.Load(targetRoom)
	if !ok {
		t.Fatal("target room queue missing")
	}
	if len(q.ch) == 0 {
		t.Fatal("expected backlog in room channel after pending full")
	}

	close(block)

	deadline := time.Now().Add(15 * time.Second)
	for {
		mu.Lock()
		seen := targetSeen
		mu.Unlock()
		if seen {
			return
		}
		svc.rescheduleBackloggedRooms()
		if time.Now().After(deadline) {
			t.Fatal("target room event never delivered")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSameRoomMultipleEventsDrainedInOrder(t *testing.T) {
	block := make(chan struct{})
	var mu sync.Mutex
	var order []int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var env envelope
		_ = json.Unmarshal(b, &env)
		data, _ := json.Marshal(env.EventData)
		var parsed roomEventData
		_ = json.Unmarshal(data, &parsed)
		mu.Lock()
		order = append(order, parsed.RoomID)
		mu.Unlock()
		<-block
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{WebhookURLs: srv.URL}
	svc := newWebhookService(t, cfg)
	const roomID = 303
	room := &bilibili.LiveRoomInfoDetail{RoomID: int64(roomID), Uname: "u", Title: "t"}

	for i := 0; i < 5; i++ {
		svc.StreamStarted(room)
	}

	close(block)

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n >= 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d deliveries", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, id := range order {
		if id != roomID {
			t.Fatalf("delivery %d room %d", i, id)
		}
	}
}
