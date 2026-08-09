package bilibili

import "testing"

func TestFindRoomInfo_ByShortIDWhenMapKeyedByLong(t *testing.T) {
	info := &LiveRoomInfoDetail{RoomID: 573893, ShortID: 545}
	infos := map[string]*LiveRoomInfoDetail{
		"573893": info,
	}

	got, ok := FindRoomInfo(infos, 545)
	if !ok || got != info {
		t.Fatalf("expected to resolve short id 545, ok=%v got=%v", ok, got)
	}

	got, ok = FindRoomInfo(infos, 573893)
	if !ok || got != info {
		t.Fatalf("expected to resolve long id 573893, ok=%v got=%v", ok, got)
	}
}

func TestFindRoomInfo_SkipsZeroShortID(t *testing.T) {
	info := &LiveRoomInfoDetail{RoomID: 100, ShortID: 0}
	infos := map[string]*LiveRoomInfoDetail{"100": info}
	if _, ok := FindRoomInfo(infos, 0); ok {
		t.Fatal("short_id=0 must not match")
	}
}
