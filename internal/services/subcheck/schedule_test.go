package subcheck

import (
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/services/subscribe"
)

func TestComputeSchedule(t *testing.T) {
	p := scheduleParamsFromConfig(50, 10, 60, 300, 32)

	tests := []struct {
		name         string
		rooms        int
		wantShards   int
		wantInterval time.Duration
	}{
		{"zero rooms", 0, 1, 60 * time.Second},
		{"one room", 1, 1, 60 * time.Second},
		{"fifty rooms", 50, 1, 60 * time.Second},
		{"fifty one rooms", 51, 2, 60 * time.Second},
		{"one hundred rooms", 100, 2, 60 * time.Second},
		{"three hundred rooms", 300, 6, 60 * time.Second},
		{"five hundred rooms", 500, 10, 100 * time.Second},
		{"sixteen hundred rooms capped", 1600, 32, 300 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeSchedule(tt.rooms, p)
			if got.shards != tt.wantShards {
				t.Fatalf("shards = %d, want %d", got.shards, tt.wantShards)
			}
			if got.interval != tt.wantInterval {
				t.Fatalf("interval = %s, want %s", got.interval, tt.wantInterval)
			}
		})
	}
}

func TestComputeSchedule_invalidParamsUseDefaults(t *testing.T) {
	p := scheduleParams{}
	got := computeSchedule(100, p)
	if got.shards != 2 {
		t.Fatalf("shards = %d, want 2", got.shards)
	}
	if got.interval != 60*time.Second {
		t.Fatalf("interval = %s, want 60s", got.interval)
	}
}

func TestCountLiveCheckRooms(t *testing.T) {
	rooms := map[int]*subscribe.RoomConfig{
		1: {Notify: true},
		2: {AutoRecord: true},
		3: {Notify: false, AutoRecord: false},
		4: nil,
	}
	if got := countLiveCheckRooms(rooms); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}
}
