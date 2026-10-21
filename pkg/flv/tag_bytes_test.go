package flv

import "testing"

func TestNextTagAndTimestamp(t *testing.T) {
	tag := NewTagBytes(TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 'K'})
	SetTagTimestamp(tag, 60_000)
	got, rest, ok, skip := NextTag(tag)
	if skip || !ok || len(rest) != 0 {
		t.Fatalf("ok=%v skip=%v rest=%d", ok, skip, len(rest))
	}
	if TagTimestamp(got) != 60_000 {
		t.Fatalf("ts=%d", TagTimestamp(got))
	}
	zero := ZeroTagTimestamp(got)
	if TagTimestamp(zero) != 0 {
		t.Fatal("zero")
	}
	if TagTimestamp(got) != 60_000 {
		t.Fatal("original mutated")
	}
}
