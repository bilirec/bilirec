package subcheck

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/services/subscribe"
)

func TestSubscribedRoomsCache_RefreshesOncePerInterval(t *testing.T) {
	const interval = time.Minute
	cache := newSubscribedRoomsCache(interval)

	var loads atomic.Int32
	load := func() (map[int]*subscribe.RoomConfig, error) {
		loads.Add(1)
		return map[int]*subscribe.RoomConfig{1: {}}, nil
	}

	base := time.Date(2026, 6, 1, 12, 0, 10, 0, time.UTC)

	for i := range 5 {
		now := base.Add(time.Duration(i*5) * time.Second)
		if _, err := cache.get(load, now); err != nil {
			t.Fatalf("get at %+v: %v", now, err)
		}
	}

	if got := loads.Load(); got != 1 {
		t.Fatalf("loads = %d, want 1 within same interval", got)
	}
}

func TestSubscribedRoomsCache_RefreshesOnNextInterval(t *testing.T) {
	const interval = time.Minute
	cache := newSubscribedRoomsCache(interval)

	var loads atomic.Int32
	load := func() (map[int]*subscribe.RoomConfig, error) {
		loads.Add(1)
		return map[int]*subscribe.RoomConfig{int(loads.Load()): {}}, nil
	}

	first := time.Date(2026, 6, 1, 12, 0, 30, 0, time.UTC)
	second := time.Date(2026, 6, 1, 12, 1, 5, 0, time.UTC)

	if _, err := cache.get(load, first); err != nil {
		t.Fatalf("first get: %v", err)
	}
	if _, err := cache.get(load, second); err != nil {
		t.Fatalf("second get: %v", err)
	}

	if got := loads.Load(); got != 2 {
		t.Fatalf("loads = %d, want 2 across interval boundary", got)
	}
}
