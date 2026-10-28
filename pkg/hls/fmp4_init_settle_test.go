package hls

import (
	"bytes"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
)

func testLog() logger.Logger {
	return logger.Nop()
}

func TestInitSettle_URIOnlySkipsRedeliver(t *testing.T) {
	var released int
	s := &InitSettle{
		Window:  50 * time.Millisecond,
		Release: func([]byte) { released++ },
		Log:     testLog(),
	}

	init1 := []byte("INIT-A")
	res := s.AcceptMap(append([]byte(nil), init1...))
	if res.DeliverNow == nil {
		t.Fatal("first init should deliver")
	}
	if s.Active() {
		t.Fatal("first init must not enter debounce")
	}

	res = s.AcceptMap(append([]byte(nil), init1...))
	if res.DeliverNow != nil || res.ArmTimer {
		t.Fatalf("URI-only must skip, got %+v", res)
	}
	if released < 1 {
		t.Fatal("URI-only map bytes should be released")
	}
}

func TestInitSettle_ChangedInitHoldsThenSettles(t *testing.T) {
	s := &InitSettle{Window: 50 * time.Millisecond, Release: func([]byte) {}, Log: testLog()}

	_ = s.AcceptMap([]byte("INIT-A"))
	pending := []byte("INIT-B")
	res := s.AcceptMap(pending)
	if !res.ArmTimer || res.DeliverNow != nil {
		t.Fatalf("changed init should hold, got %+v", res)
	}
	if !s.Active() {
		t.Fatal("expected active settle")
	}
	s.BufferMedia([]byte("MOOF1"))
	s.BufferMedia([]byte("MOOF2"))

	initOut, media := s.Settle()
	if !bytes.Equal(initOut, []byte("INIT-B")) {
		t.Fatalf("settle init=%q", initOut)
	}
	if &initOut[0] != &pending[0] {
		t.Fatal("settle should transfer held pending buffer, not copy")
	}
	if len(media) != 2 {
		t.Fatalf("expected 2 media, got %d", len(media))
	}
	if s.Active() {
		t.Fatal("settle should clear active")
	}
}

func TestInitSettle_ChurnBackToConfirmedDropsBuf(t *testing.T) {
	var released int
	var releasedPending bool
	pendingB := []byte("INIT-B")
	s := &InitSettle{
		Window: 2 * time.Second,
		Release: func(b []byte) {
			released++
			if len(b) > 0 && &b[0] == &pendingB[0] {
				releasedPending = true
			}
		},
		Log: testLog(),
	}
	_ = s.AcceptMap([]byte("INIT-A"))
	_ = s.AcceptMap(pendingB)
	s.BufferMedia([]byte("MOOF-B"))

	res := s.AcceptMap([]byte("INIT-A"))
	if res.ArmTimer || res.DeliverNow != nil {
		t.Fatalf("back to confirmed should cancel, got %+v", res)
	}
	if s.Active() {
		t.Fatal("expected inactive after cancel")
	}
	if released < 2 {
		t.Fatal("pending init and buffered media should be released on cancel")
	}
	if !releasedPending {
		t.Fatal("held pending init should be released on cancel")
	}
}

func TestInitSettle_MuxCompatibleSkipsDebounce(t *testing.T) {
	s := &InitSettle{Window: 50 * time.Millisecond, Release: func([]byte) {}, Log: testLog()}

	initA := buildTestInit(1000, 1280<<16, []byte{0x01, 0x02}, 0)
	res := s.AcceptMap(append([]byte(nil), initA...))
	if res.DeliverNow == nil || res.ArmTimer {
		t.Fatalf("first init: %+v", res)
	}

	initA2 := buildTestInit(9999, 1280<<16, []byte{0x01, 0x02}, 0)
	res = s.AcceptMap(append([]byte(nil), initA2...))
	if res.DeliverNow != nil || res.ArmTimer || s.Active() {
		t.Fatalf("mux-compatible should skip debounce, got %+v active=%v", res, s.Active())
	}
}

func TestInitSettle_RememberMapAndAcceptCached(t *testing.T) {
	var released int
	s := &InitSettle{
		Window:  50 * time.Millisecond,
		Release: func([]byte) { released++ },
		Log:     testLog(),
	}
	init1 := []byte("INIT-A")
	s.RememberMap("map-a", init1)
	res, ok := s.AcceptCachedMap("map-a")
	if !ok {
		t.Fatal("expected cached map")
	}
	if res.DeliverNow == nil {
		t.Fatal("first cached accept should deliver")
	}
	if released != 0 {
		t.Fatal("cached accept must not release to pool")
	}

	res, ok = s.AcceptCachedMap("map-a")
	if !ok {
		t.Fatal("expected cached map")
	}
	if res.DeliverNow != nil || res.ArmTimer {
		t.Fatalf("URI-only cached should skip, got %+v", res)
	}
	if released != 0 {
		t.Fatal("cached skip must not release")
	}
}

func TestInitSettle_ResetClearsMapCache(t *testing.T) {
	s := &InitSettle{Release: func([]byte) {}, Log: testLog()}
	s.RememberMap("u", []byte("x"))
	s.Reset("test")
	if s.HasCachedMap("u") {
		t.Fatal("Reset should clear map cache")
	}
}
