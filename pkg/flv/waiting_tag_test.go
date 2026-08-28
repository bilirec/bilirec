package flv

import "testing"

func TestIsWaitingForPartialTag_HugeDataSizeDoesNotBlockTrim(t *testing.T) {
	hdr := make([]byte, PrevTagSizeBytes+TagHeaderSize)
	hdr[PrevTagSizeBytes] = TagTypeVideo
	hdr[PrevTagSizeBytes+1] = 0xFF
	hdr[PrevTagSizeBytes+2] = 0xFF
	hdr[PrevTagSizeBytes+3] = 0xFF
	if isWaitingForPartialTag(hdr) {
		t.Fatal("UI24-max dataSize should not block trim")
	}

	hdr[PrevTagSizeBytes+1] = 0x09
	hdr[PrevTagSizeBytes+2] = 0x60
	hdr[PrevTagSizeBytes+3] = 0x00 // 600KiB
	if !isWaitingForPartialTag(hdr) {
		t.Fatal("incomplete 600KiB video tag should still wait")
	}
}

func TestRealtimeFixer_HugeClaimedTag_TrimmedToMaxBuffer(t *testing.T) {
	fixer := NewRealtimeFixer()
	defer fixer.Close()

	if _, err := fixer.Fix(FlvHeader); err != nil {
		t.Fatalf("header: %v", err)
	}

	hdr := make([]byte, PrevTagSizeBytes+TagHeaderSize)
	hdr[PrevTagSizeBytes] = TagTypeVideo
	hdr[PrevTagSizeBytes+1] = 0xFF
	hdr[PrevTagSizeBytes+2] = 0xFF
	hdr[PrevTagSizeBytes+3] = 0xFF
	payload := make([]byte, 2*MaxBufferSize)
	input := append(hdr, payload...)
	if _, err := fixer.Fix(input); err != nil {
		t.Fatalf("fix: %v", err)
	}

	fixer.mu.Lock()
	n := fixer.buffer.Len()
	fixer.mu.Unlock()
	if n > MaxBufferSize {
		t.Fatalf("parse buffer grew to %d, want <= %d after trim", n, MaxBufferSize)
	}
}
