package sei

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestAnnexBTrailingStartCodeDoesNotHang(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		cases := [][]byte{
			{0, 0, 0, 1, 0x65, 0, 0, 1},
			{0, 0, 1, 0x65, 0, 0, 0, 1},
			{0, 0, 1},
			{0, 0, 0, 1},
		}
		for _, nals := range cases {
			ForEachRBSP(nals, AVC, func(_ []byte) {})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("annex-b scan hung on trailing start code")
	}
}

func TestUserDataUnregistered_type5(t *testing.T) {
	uuid := []byte("1234567890123456")
	body := append(uuid, []byte("tail")...)
	rbsp := buildSEIRBSP(5, body)
	got := UserDataUnregistered(rbsp)
	if len(got) != 1 || len(got[0]) < 16 {
		t.Fatalf("unexpected payloads: %d", len(got))
	}
}

func buildSEIRBSP(payloadType int, payload []byte) []byte {
	var out []byte
	out = append(out, byte(payloadType))
	out = append(out, byte(len(payload)))
	out = append(out, payload...)
	out = append(out, 0x80)
	return out
}

func TestForEachRBSP_AVCSEI(t *testing.T) {
	payload := []byte("1234567890123456data")
	nal := buildAVCSEINAL(payload)
	var count int
	ForEachRBSP(nal, AVC, func(rbsp []byte) {
		count++
		if len(UserDataUnregistered(rbsp)) == 0 {
			t.Fatal("expected type 5 payload")
		}
	})
	if count != 1 {
		t.Fatalf("expected 1 SEI NAL, got %d", count)
	}
}

func buildAVCSEINAL(payload []byte) []byte {
	rbsp := buildSEIRBSP(5, payload)
	nal := append([]byte{0x06}, rbsp...)
	var out []byte
	out = binary.BigEndian.AppendUint32(out, uint32(len(nal)))
	out = append(out, nal...)
	return out
}
