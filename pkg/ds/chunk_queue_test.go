package ds

import (
	"sync"
	"testing"
	"time"
)

func TestChunkQueue_rejectsWhenFullUnlessEmpty(t *testing.T) {
	q := NewChunkQueue(100, func(b []byte) int { return len(b) })
	if !q.CanTake(50) {
		t.Fatal("empty queue should accept 50")
	}
	if !q.TryPush(make([]byte, 50)) {
		t.Fatal("push 50")
	}

	if q.CanTake(51) {
		t.Fatal("should not accept 51 when 50 queued and max 100")
	}
	if !q.CanTake(50) {
		t.Fatal("should accept 50 when 50 queued")
	}
	if q.CanTake(200) {
		t.Fatal("should not accept 200 when 50 already queued")
	}

	q2 := NewChunkQueue(100, func(b []byte) int { return len(b) }, AllowOversizeWhenEmpty())
	if !q2.CanTake(200) {
		t.Fatal("empty-queue rule: single chunk over max should be accepted when queue empty")
	}
}

func TestChunkQueue_allowOversizeWhenEmpty(t *testing.T) {
	q := NewChunkQueue(100, func(b []byte) int { return len(b) }, AllowOversizeWhenEmpty())
	if !q.TryPush(make([]byte, 200)) {
		t.Fatal("empty queue should accept oversize chunk")
	}
	if q.TryPush(make([]byte, 1)) {
		t.Fatal("should reject when next chunk would exceed max")
	}
}

func TestChunkQueue_popWaitsUntilPushOrClose(t *testing.T) {
	q := NewChunkQueue(100, func(b []byte) int { return len(b) })
	var got []byte
	var ok bool
	done := make(chan struct{})
	go func() {
		got, ok = q.Pop()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("pop returned before push")
	case <-time.After(50 * time.Millisecond):
	}

	if !q.TryPush([]byte{1, 2}) {
		t.Fatal("push")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pop did not unblock after push")
	}
	if !ok || len(got) != 2 {
		t.Fatalf("unexpected pop: ok=%v got=%v", ok, got)
	}
}

func TestChunkQueue_closeIsIdempotent(t *testing.T) {
	q := NewChunkQueue(100, func(b []byte) int { return len(b) })
	q.Close()
	q.Close()
	if q.TryPush([]byte{1}) {
		t.Fatal("push after close")
	}
	item, ok := q.Pop()
	if ok || len(item) != 0 {
		t.Fatalf("expected drained close, got ok=%v len=%d", ok, len(item))
	}
}

func TestChunkQueue_concurrentTryPush(t *testing.T) {
	q := NewChunkQueue(1000, func(b []byte) int { return len(b) })
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				q.TryPush([]byte{byte(j)})
			}
		}()
	}
	wg.Wait()
}
