package subscribe

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

func TestCloneRoomConfigIsolatesCaller(t *testing.T) {
	original := &RoomConfig{
		AutoRecord:     true,
		StreamProfiles: []string{string(bilibili.ProfileHLSFMP4)},
	}

	cloned := cloneRoomConfig(original)
	cloned.AutoRecord = false
	cloned.StreamProfiles[0] = string(bilibili.ProfileHTTPFLV)
	cloned.StreamProfiles = append(cloned.StreamProfiles, string(bilibili.ProfileHLSTS))

	if !original.AutoRecord {
		t.Fatal("clone mutated original AutoRecord")
	}
	if original.StreamProfiles[0] != string(bilibili.ProfileHLSFMP4) || len(original.StreamProfiles) != 1 {
		t.Fatalf("clone mutated original StreamProfiles: %v", original.StreamProfiles)
	}

	if got := cloneRoomConfig(nil); got.AutoRecord || got.Notify || got.RecordDurationMinutes != 0 {
		t.Fatalf("nil clone should be default, got %+v", got)
	}
}

func TestCloneRoomsIsolatesMapAndConfigs(t *testing.T) {
	original := map[int]*RoomConfig{
		1: {
			AutoRecord:     true,
			Notify:         true,
			StreamProfiles: []string{string(bilibili.ProfileHLSFMP4)},
		},
		2: {
			RecordDanmaku: true,
		},
	}

	cloned := cloneRooms(original)
	delete(cloned, 1)
	cloned[2].RecordDanmaku = false
	cloned[3] = &RoomConfig{OnlyAudio: true}

	if _, ok := original[1]; !ok {
		t.Fatal("deleting cloned map key removed the original room")
	}
	if _, ok := original[3]; ok {
		t.Fatal("inserting into cloned map leaked into the original")
	}
	if !original[2].RecordDanmaku {
		t.Fatal("mutating cloned config mutated the original")
	}

	again := cloneRooms(original)
	again[1].StreamProfiles[0] = string(bilibili.ProfileHTTPFLV)
	again[1].StreamProfiles = append(again[1].StreamProfiles, string(bilibili.ProfileHLSTS))
	if original[1].StreamProfiles[0] != string(bilibili.ProfileHLSFMP4) || len(original[1].StreamProfiles) != 1 {
		t.Fatalf("mutating cloned StreamProfiles mutated the original: %v", original[1].StreamProfiles)
	}

	if got := cloneRooms(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil source should clone to empty map, got %#v", got)
	}

	withNil := cloneRooms(map[int]*RoomConfig{9: nil})
	if withNil[9] == nil || withNil[9].AutoRecord || withNil[9].Notify {
		t.Fatalf("nil room config should clone to default, got %+v", withNil[9])
	}
}
