package room

import (
	"fmt"
	"testing"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/pkg/swr"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func newTestService(t *testing.T, softTTL, hardTTL time.Duration) *Service {
	t.Helper()

	svc := &Service{
		cache: swr.NewCache[string, *bilibili.LiveRoomInfoDetail](softTTL, hardTTL, 16),
	}

	svc.cache.Start()
	t.Cleanup(func() {
		svc.cache.DeleteAll()
		svc.cache.Stop()
	})

	return svc
}

func TestCache_CacheHit(t *testing.T) {
	svc := newTestService(t, 100*time.Millisecond, time.Second)
	roomID := 10086
	key := fmt.Sprint(roomID)
	expected := &bilibili.LiveRoomInfoDetail{RoomID: int64(roomID), LiveStatus: 1, Title: "cached-room"}

	svc.cache.Set(key, expected)

	got, err := svc.GetLiveRoomInfo(roomID)
	if err != nil {
		t.Fatalf("GetLiveRoomInfo returned error: %v", err)
	}
	if got != expected {
		t.Fatalf("cache hit should return cached pointer, got=%p want=%p", got, expected)
	}

	got2, err := svc.GetLiveRoomInfo(roomID)
	if err != nil {
		t.Fatalf("second GetLiveRoomInfo returned error: %v", err)
	}
	if got2 != expected {
		t.Fatalf("second cache hit should return cached pointer, got=%p want=%p", got2, expected)
	}
}

func TestCache_CacheTTL(t *testing.T) {
	softTTL := 30 * time.Millisecond
	hardTTL := 90 * time.Millisecond
	svc := newTestService(t, softTTL, hardTTL)

	roomID := 9527
	key := fmt.Sprint(roomID)
	info := &bilibili.LiveRoomInfoDetail{RoomID: int64(roomID), LiveStatus: 1}
	svc.cache.Set(key, info)

	_, ok, stale := svc.cache.Get(key)
	if !ok {
		t.Fatal("cache should have value immediately after Set")
	}
	if stale {
		t.Fatal("cache should not be stale immediately after Set")
	}

	time.Sleep(softTTL + 20*time.Millisecond)
	_, ok, stale = svc.cache.Get(key)
	if !ok {
		t.Fatal("cache should still exist after soft TTL")
	}
	if !stale {
		t.Fatal("cache should be stale after soft TTL")
	}

	time.Sleep(hardTTL + 30*time.Millisecond)
	_, ok, _ = svc.cache.Get(key)
	if ok {
		t.Fatal("cache should expire after hard TTL")
	}
}

func TestCache_Fx(t *testing.T) {

	var svc *Service
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(NewService),
		fx.Populate(&svc),
		fx.StartTimeout(5*time.Second),
		fx.StopTimeout(15*time.Second),
	)
	app.RequireStart()
	defer app.RequireStop()

	if svc == nil {
		t.Fatal("failed to initialize Service via Fx")
	}

	t.Log("Service initialized successfully via Fx")
}
