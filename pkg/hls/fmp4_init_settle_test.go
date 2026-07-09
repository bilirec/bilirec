package hls

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func testLog() *logrus.Entry {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return logrus.NewEntry(l)
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
	res := s.AcceptMap([]byte("INIT-B"))
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
	if len(media) != 2 {
		t.Fatalf("expected 2 media, got %d", len(media))
	}
	if s.Active() {
		t.Fatal("settle should clear active")
	}
}

func TestInitSettle_ChurnBackToConfirmedDropsBuf(t *testing.T) {
	var released int
	s := &InitSettle{
		Window:  2 * time.Second,
		Release: func([]byte) { released++ },
		Log:     testLog(),
	}
	_ = s.AcceptMap([]byte("INIT-A"))
	_ = s.AcceptMap([]byte("INIT-B"))
	s.BufferMedia([]byte("MOOF-B"))

	res := s.AcceptMap([]byte("INIT-A"))
	if res.ArmTimer || res.DeliverNow != nil {
		t.Fatalf("back to confirmed should cancel, got %+v", res)
	}
	if s.Active() {
		t.Fatal("expected inactive after cancel")
	}
	if released < 1 {
		t.Fatal("buffered media should be released on cancel")
	}
}
