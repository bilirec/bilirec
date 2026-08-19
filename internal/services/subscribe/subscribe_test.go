package subscribe_test

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/bilirec/bilirec/internal/testutil"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
	if os.Getenv("CI") != "" {
		os.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
	}
}

func startSubscribeService(t *testing.T, dir string) (*subscribe.Service, *fxtest.App) {
	t.Helper()

	t.Setenv("DATABASE_DIR", dir)

	var svc *subscribe.Service
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(room.NewService),
		fx.Provide(subscribe.NewService),
		fx.Populate(&svc),
		fx.StartTimeout(5*time.Second),
	)
	app.RequireStart()
	t.Cleanup(func() {
		app.RequireStop()
	})
	return svc, app
}

func newSubscribeService(t *testing.T) *subscribe.Service {
	t.Helper()
	svc, _ := startSubscribeService(t, t.TempDir())
	return svc
}

func TestPubsub_Subscribe(t *testing.T) {
	svc := newSubscribeService(t)

	existing, _ := svc.ListSubscribedRooms()
	for _, rid := range existing {
		_ = svc.Unsubscribe(rid)
	}

	testRoomID := testutil.LiveRoomID(t)
	err := svc.Subscribe(testRoomID)
	if err != nil {
		t.Fatalf("first subscribe failed: %v", err)
	}

	err = svc.Subscribe(testRoomID)
	if err != subscribe.ErrRoomAlreadySubscribed {
		t.Fatalf("expected ErrRoomAlreadySubscribed, got: %v", err)
	}

	if err := svc.Unsubscribe(testRoomID); err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	err = svc.Subscribe(testRoomID)
	if err != nil {
		t.Fatalf("resubscribe after unsubscribe failed: %v", err)
	}
}

func TestPubsub_Unsubscribe(t *testing.T) {
	svc := newSubscribeService(t)
	testRoomID := testutil.LiveRoomID(t)

	err := svc.Unsubscribe(testRoomID)
	if err != subscribe.ErrRoomNotSubscribed {
		t.Fatalf("expected ErrRoomNotSubscribed, got: %v", err)
	}

	if err := svc.Subscribe(testRoomID); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	err = svc.Unsubscribe(testRoomID)
	if err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	err = svc.Unsubscribe(testRoomID)
	if err != subscribe.ErrRoomNotSubscribed {
		t.Fatalf("expected ErrRoomNotSubscribed on second unsubscribe, got: %v", err)
	}
}

func TestPubsub_IsSubscribed(t *testing.T) {
	svc := newSubscribeService(t)
	testRoomID := testutil.LiveRoomID(t)

	isSubscribed, err := svc.IsSubscribed(testRoomID)
	if err != nil {
		t.Fatalf("IsSubscribed failed: %v", err)
	}
	if isSubscribed {
		t.Fatal("expected not subscribed before subscription")
	}

	if err := svc.Subscribe(testRoomID); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	isSubscribed, err = svc.IsSubscribed(testRoomID)
	if err != nil {
		t.Fatalf("IsSubscribed failed: %v", err)
	}
	if !isSubscribed {
		t.Fatal("expected subscribed after subscription")
	}

	if err := svc.Unsubscribe(testRoomID); err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	isSubscribed, err = svc.IsSubscribed(testRoomID)
	if err != nil {
		t.Fatalf("IsSubscribed failed: %v", err)
	}
	if isSubscribed {
		t.Fatal("expected not subscribed after unsubscription")
	}
}

func TestPubsub_ListSubscribedRooms(t *testing.T) {
	svc := newSubscribeService(t)

	existingRooms, err := svc.ListSubscribedRooms()
	if err != nil {
		t.Fatalf("ListSubscribedRooms failed: %v", err)
	}
	for _, rid := range existingRooms {
		_ = svc.Unsubscribe(rid)
	}

	rooms, err := svc.ListSubscribedRooms()
	if err != nil {
		t.Fatalf("ListSubscribedRooms failed: %v", err)
	}
	if len(rooms) != 0 {
		t.Fatalf("expected empty list after cleanup, got %d rooms", len(rooms))
	}

	testRooms := testutil.LiveRoomIDs(t, 3)
	for _, roomID := range testRooms {
		if err := svc.Subscribe(roomID); err != nil {
			t.Fatalf("subscribe %d failed: %v", roomID, err)
		}
	}

	rooms, err = svc.ListSubscribedRooms()
	if err != nil {
		t.Fatalf("ListSubscribedRooms failed: %v", err)
	}
	if len(rooms) != len(testRooms) {
		t.Fatalf("expected %d rooms, got %d", len(testRooms), len(rooms))
	}

	roomMap := make(map[int]bool)
	for _, rid := range rooms {
		roomMap[rid] = true
	}
	for _, expectedRoom := range testRooms {
		if !roomMap[expectedRoom] {
			t.Fatalf("room %d not in list", expectedRoom)
		}
	}

	for _, rid := range testRooms {
		if err := svc.Unsubscribe(rid); err != nil {
			t.Fatalf("unsubscribe failed: %v", err)
		}
	}
}

func TestMemoryLeak_SubscribeUnsubscribeCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory test in short mode")
	}

	svc := newSubscribeService(t)
	var m1, m2, m3 runtime.MemStats

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.ReadMemStats(&m1)

	const iterations = 200
	roomIDs := testutil.LiveRoomIDs(t, iterations)
	for i := 0; i < iterations; i++ {
		roomID := roomIDs[i]
		if err := svc.Subscribe(roomID); err != nil {
			t.Fatalf("subscribe failed: %v", err)
		}
		if err := svc.Unsubscribe(roomID); err != nil {
			t.Fatalf("unsubscribe failed: %v", err)
		}
	}

	runtime.ReadMemStats(&m2)
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
	runtime.ReadMemStats(&m3)

	baselineAlloc := float64(m1.Alloc) / (1024 * 1024)
	afterGCAlloc := float64(m3.Alloc) / (1024 * 1024)
	retainedAfterGC := afterGCAlloc - baselineAlloc

	const maxRetainedMB = 5.0
	if retainedAfterGC > maxRetainedMB {
		t.Errorf("possible memory leak: %.2f MB retained after GC", retainedAfterGC)
	}
}

func TestMemoryLeak_ListCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory test in short mode")
	}

	svc := newSubscribeService(t)
	var m1, m2, m3 runtime.MemStats

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.ReadMemStats(&m1)

	const iterations = 200
	for i := 0; i < iterations; i++ {
		if _, err := svc.ListSubscribedRooms(); err != nil {
			t.Fatalf("ListSubscribedRooms failed: %v", err)
		}
	}

	runtime.ReadMemStats(&m2)
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
	runtime.ReadMemStats(&m3)

	baselineAlloc := float64(m1.Alloc) / (1024 * 1024)
	afterGCAlloc := float64(m3.Alloc) / (1024 * 1024)
	retainedAfterGC := afterGCAlloc - baselineAlloc

	const maxRetainedMB = 5.0
	if retainedAfterGC > maxRetainedMB {
		t.Errorf("possible memory leak: %.2f MB retained after GC", retainedAfterGC)
	}
}

func TestSnapshot_WriteThroughVisibleImmediately(t *testing.T) {
	svc := newSubscribeService(t)
	roomID := testutil.LiveRoomID(t)

	if err := svc.Subscribe(roomID); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	rooms, err := svc.ListSubscribedRoomsWithConfig()
	if err != nil {
		t.Fatalf("list with config failed: %v", err)
	}
	cfg, ok := rooms[roomID]
	if !ok || cfg == nil {
		t.Fatal("expected subscribed room in snapshot immediately")
	}
	if cfg.AutoRecord || cfg.Notify {
		t.Fatalf("expected default flags, got %+v", cfg)
	}

	updated := &subscribe.RoomConfig{
		AutoRecord:            true,
		Notify:                true,
		RecordDurationMinutes: 45,
		RecordDanmaku:         true,
		StreamProfiles:        []string{"hls-fmp4"},
	}
	if err := svc.UpdateConfig(roomID, updated); err != nil {
		t.Fatalf("update config failed: %v", err)
	}

	got, err := svc.GetConfig(roomID)
	if err != nil {
		t.Fatalf("get config failed: %v", err)
	}
	if !got.AutoRecord || !got.Notify || got.RecordDurationMinutes != 45 || !got.RecordDanmaku {
		t.Fatalf("snapshot did not pick up update: %+v", got)
	}
	if len(got.StreamProfiles) != 1 || got.StreamProfiles[0] != "hls-fmp4" {
		t.Fatalf("snapshot stream profiles = %v", got.StreamProfiles)
	}

	got.AutoRecord = false
	got.StreamProfiles[0] = "http-flv"
	got.StreamProfiles = append(got.StreamProfiles, "hls-ts")

	again, err := svc.GetConfig(roomID)
	if err != nil {
		t.Fatalf("get config again failed: %v", err)
	}
	if !again.AutoRecord || again.StreamProfiles[0] != "hls-fmp4" || len(again.StreamProfiles) != 1 {
		t.Fatalf("caller mutated cached config: %+v", again)
	}

	if err := svc.Unsubscribe(roomID); err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}
	if _, err := svc.GetConfig(roomID); err != subscribe.ErrRoomNotSubscribed {
		t.Fatalf("expected ErrRoomNotSubscribed after unsubscribe, got %v", err)
	}
	listed, err := svc.ListSubscribedRooms()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	for _, id := range listed {
		if id == roomID {
			t.Fatal("unsubscribed room still in snapshot")
		}
	}
}

func TestSnapshot_ReloadsAfterRestart(t *testing.T) {
	dir := t.TempDir()
	roomID := testutil.LiveRoomID(t)

	svc, app := startSubscribeService(t, dir)
	if err := svc.Subscribe(roomID); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if err := svc.UpdateConfig(roomID, &subscribe.RoomConfig{
		AutoRecord: true,
		Notify:     true,
		Qn:         10000,
	}); err != nil {
		t.Fatalf("update config failed: %v", err)
	}
	app.RequireStop()

	svc, _ = startSubscribeService(t, dir)
	got, err := svc.GetConfig(roomID)
	if err != nil {
		t.Fatalf("get config after restart failed: %v", err)
	}
	if !got.AutoRecord || !got.Notify || got.Qn != 10000 {
		t.Fatalf("snapshot did not reload from bolt: %+v", got)
	}
}

func TestConcurrency_SubscribeAndListIsolated(t *testing.T) {
	svc := newSubscribeService(t)
	done := make(chan bool, 20)
	roomIDs := testutil.LiveRoomIDs(t, 40)

	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < 10; j++ {
				roomIndex := (id * 4) + (j % 4)
				roomID := roomIDs[roomIndex%len(roomIDs)]
				_ = svc.Subscribe(roomID)
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}

	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			for j := 0; j < 50; j++ {
				if _, err := svc.ListSubscribedRooms(); err != nil {
					t.Errorf("ListSubscribedRooms failed: %v", err)
					return
				}
				time.Sleep(1 * time.Millisecond)
			}
		}()
	}

	for i := 0; i < 20; i++ {
		<-done
	}
}
