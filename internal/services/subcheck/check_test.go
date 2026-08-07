package subcheck

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/puzpuzpuz/xsync/v4"
)

func TestStreamOptionsFromRoomConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  *subscribe.RoomConfig
		want int
	}{
		{name: "nil config", cfg: nil, want: 0},
		{name: "defaults", cfg: &subscribe.RoomConfig{}, want: 0},
		{name: "only audio", cfg: &subscribe.RoomConfig{OnlyAudio: true}, want: 1},
		{name: "quality", cfg: &subscribe.RoomConfig{Qn: int(bilibili.QualityHigh)}, want: 1},
		{name: "quality and audio", cfg: &subscribe.RoomConfig{Qn: int(bilibili.QualityHigh), OnlyAudio: true}, want: 2},
		{name: "invalid quality ignored", cfg: &subscribe.RoomConfig{Qn: 999}, want: 0},
		{name: "stream profile", cfg: &subscribe.RoomConfig{StreamProfiles: []string{string(bilibili.ProfileHLSFMP4)}}, want: 1},
		{name: "multi stream profiles", cfg: &subscribe.RoomConfig{StreamProfiles: []string{"hls-fmp4", "hls-ts"}}, want: 1},
		{name: "invalid stream profile ignored", cfg: &subscribe.RoomConfig{StreamProfiles: []string{"dash"}}, want: 0},
		{name: "profile quality audio", cfg: &subscribe.RoomConfig{Qn: int(bilibili.QualityHigh), OnlyAudio: true, StreamProfiles: []string{string(bilibili.ProfileHLSTS)}}, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := streamOptionsFromRoomConfig(tt.cfg)
			if len(got) != tt.want {
				t.Fatalf("got %d stream options, want %d", len(got), tt.want)
			}
		})
	}
}

func TestResolveLiveSessionKey(t *testing.T) {
	tests := []struct {
		name string
		info *bilibili.LiveRoomInfoDetail
		want string
	}{
		{
			name: "prefer LiveIDStr",
			info: &bilibili.LiveRoomInfoDetail{LiveIDStr: "abc", LiveID: 123, LiveTime: "2026-05-06 12:00:00"},
			want: "live_id_str:abc",
		},
		{
			name: "fallback to LiveID",
			info: &bilibili.LiveRoomInfoDetail{LiveID: 123, LiveTime: "2026-05-06 12:00:00"},
			want: "live_id:123",
		},
		{
			name: "fallback to LiveTime",
			info: &bilibili.LiveRoomInfoDetail{LiveTime: "2026-05-06 12:00:00"},
			want: "live_time:2026-05-06 12:00:00",
		},
		{
			name: "ignore placeholder ids and time",
			info: &bilibili.LiveRoomInfoDetail{LiveIDStr: "0", LiveID: 0, LiveTime: "0000-00-00 00:00:00"},
			want: "",
		},
		{
			name: "fallback to live_time when id_str is placeholder",
			info: &bilibili.LiveRoomInfoDetail{LiveIDStr: "0", LiveID: 0, LiveTime: "2026-05-06 17:49:44"},
			want: "live_time:2026-05-06 17:49:44",
		},
		{
			name: "empty when no session fields",
			info: &bilibili.LiveRoomInfoDetail{},
			want: "",
		},
		{
			name: "empty when nil info",
			info: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLiveSessionKey(tt.info); got != tt.want {
				t.Fatalf("resolveLiveSessionKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionState_MarkAndClear(t *testing.T) {
	service := newTestServiceWithBucket(t)

	const roomID = 1001
	const key = "live_id_str:test-session-1"

	service.markSessionState(roomID, key)

	stored, loaded := service.sessionKeys.Load(roomID)
	if !loaded || stored != key {
		t.Fatalf("session key in memory = (%q, %v), want (%q, true)", stored, loaded, key)
	}

	dbValue, err := service.bucket.Get([]byte(strconv.Itoa(roomID)))
	if err != nil {
		t.Fatalf("failed to read session key from bucket: %v", err)
	}
	if string(dbValue) != key {
		t.Fatalf("session key in bucket = %q, want %q", string(dbValue), key)
	}

	service.clearSessionState(roomID)

	if _, loaded = service.sessionKeys.Load(roomID); loaded {
		t.Fatal("expected in-memory session key to be removed")
	}

	exists, err := service.bucket.Exists([]byte(strconv.Itoa(roomID)))
	if err != nil {
		t.Fatalf("failed to check session key existence in bucket: %v", err)
	}
	if exists {
		t.Fatal("expected session key in bucket to be removed")
	}
}

func TestSessionState_InvalidateStaleRooms(t *testing.T) {
	service := newTestServiceWithBucket(t)

	service.markSessionState(1, "live_id_str:keep-notify")
	service.markSessionState(2, "live_id_str:keep-auto")
	service.markSessionState(3, "live_id_str:remove-disabled")
	service.markSessionState(4, "live_id_str:remove-missing")

	rooms := map[int]*subscribe.RoomConfig{
		1: {Notify: true, AutoRecord: false},
		2: {Notify: false, AutoRecord: true},
		3: {Notify: false, AutoRecord: false},
	}

	service.invalidateStaleRooms(rooms)

	if _, ok := service.sessionKeys.Load(1); !ok {
		t.Fatal("room 1 should be kept")
	}
	if _, ok := service.sessionKeys.Load(2); !ok {
		t.Fatal("room 2 should be kept")
	}
	if _, ok := service.sessionKeys.Load(3); ok {
		t.Fatal("room 3 should be removed")
	}
	if _, ok := service.sessionKeys.Load(4); ok {
		t.Fatal("room 4 should be removed")
	}

	for _, roomID := range []int{1, 2} {
		exists, err := service.bucket.Exists([]byte(strconv.Itoa(roomID)))
		if err != nil {
			t.Fatalf("failed to check bucket key existence for room %d: %v", roomID, err)
		}
		if !exists {
			t.Fatalf("room %d should remain in bucket", roomID)
		}
	}

	for _, roomID := range []int{3, 4} {
		exists, err := service.bucket.Exists([]byte(strconv.Itoa(roomID)))
		if err != nil {
			t.Fatalf("failed to check bucket key existence for room %d: %v", roomID, err)
		}
		if exists {
			t.Fatalf("room %d should be removed from bucket", roomID)
		}
	}
}

func TestSessionState_SameSessionNotReprocessed(t *testing.T) {
	service := newTestServiceWithBucket(t)

	const roomID = 3001
	const sessionKey = "live_id_str:session-abc"

	// 第一次新 session：標記狀態
	service.markSessionState(roomID, sessionKey)

	stored, loaded := service.sessionKeys.Load(roomID)
	if !loaded || stored != sessionKey {
		t.Fatal("first session mark should succeed")
	}

	// 模擬下一輪檢查：相同 key 應該被跳過（不重新執行錄製/通知）
	storedKey, loaded := service.sessionKeys.Load(roomID)
	if !(loaded && storedKey == sessionKey) {
		t.Fatal("same session key should be detected for skipping")
	}

	// 驗證 DB 中也有該 key
	dbValue, err := service.bucket.Get([]byte(strconv.Itoa(roomID)))
	if err != nil {
		t.Fatalf("failed to read from bucket: %v", err)
	}
	if string(dbValue) != sessionKey {
		t.Fatalf("DB session key = %q, want %q", string(dbValue), sessionKey)
	}
}

func TestSessionState_DifferentSessionIsNewLive(t *testing.T) {
	service := newTestServiceWithBucket(t)

	const roomID = 3002
	const sessionKey1 = "live_id_str:session-1"
	const sessionKey2 = "live_id_str:session-2"

	// 第一場直播：標記 session 1
	service.markSessionState(roomID, sessionKey1)

	stored, loaded := service.sessionKeys.Load(roomID)
	if !loaded || stored != sessionKey1 {
		t.Fatal("session 1 should be marked")
	}

	// 檢查：key 相同會被跳過
	if stored == sessionKey1 {
		// 邏輯層會 continue，不重新處理
		t.Log("session 1 detected as processed (would skip)")
	}

	// 模擬新直播開始（key 變化）
	service.markSessionState(roomID, sessionKey2)

	stored, loaded = service.sessionKeys.Load(roomID)
	if !loaded || stored != sessionKey2 {
		t.Fatal("session 2 should replace session 1")
	}

	// 驗證：新 key 不等於舊 key，會被視為新 session，會重新執行
	if stored != sessionKey1 && stored == sessionKey2 {
		t.Log("session 2 detected as new (would reprocess)")
	}
}

func TestSessionState_LiveEndedAndNewSessionCycle(t *testing.T) {
	service := newTestServiceWithBucket(t)

	const roomID = 3003
	const sessionKey1 = "live_id_str:live-1"
	const sessionKey2 = "live_id_str:live-2"

	// Live 1: 標記 session
	service.markSessionState(roomID, sessionKey1)

	stored, loaded := service.sessionKeys.Load(roomID)
	if !loaded || stored != sessionKey1 {
		t.Fatal("live 1 should be marked")
	}

	// Live 1: 下播，清除狀態
	service.clearSessionState(roomID)

	_, loaded = service.sessionKeys.Load(roomID)
	if loaded {
		t.Fatal("live 1 should be cleared after broadcast ended")
	}

	exists, err := service.bucket.Exists([]byte(strconv.Itoa(roomID)))
	if err != nil {
		t.Fatalf("failed to check bucket: %v", err)
	}
	if exists {
		t.Fatal("live 1 should be removed from DB")
	}

	// Live 2: 新直播開始
	service.markSessionState(roomID, sessionKey2)

	stored, loaded = service.sessionKeys.Load(roomID)
	if !loaded || stored != sessionKey2 {
		t.Fatal("live 2 should be marked")
	}

	// 驗證：live 2 是新 session，會被重新處理
	if stored != sessionKey1 && stored == sessionKey2 {
		t.Log("live 2 detected as new session (would reprocess)")
	}
}

func newTestServiceWithBucket(t *testing.T) *Service {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "subcheck-test.db")
	client, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	bucket, err := client.Bucket(sessionKeysBucketName)
	if err != nil {
		t.Fatalf("failed to open test bucket: %v", err)
	}

	return &Service{
		m:           &metrics.Exporter{},
		bucket:      bucket,
		sessionKeys: xsync.NewMap[int, string](),
	}
}
