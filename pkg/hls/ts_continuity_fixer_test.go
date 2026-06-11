package hls

import "testing"

const (
	testTsPacketSize = 188
	testTsSyncByte   = 0x47
)

func makeTSPacket(pid uint16, adaptationFieldControl uint8, cc uint8, discontinuity bool) []byte {
	pkt := make([]byte, testTsPacketSize)
	pkt[0] = testTsSyncByte
	pkt[1] = byte((pid >> 8) & 0x1F)
	pkt[2] = byte(pid & 0xFF)
	pkt[3] = ((adaptationFieldControl & 0x3) << 4) | (cc & 0x0F)

	if adaptationFieldControl == 0x3 {
		pkt[4] = 1 // adaptation field length
		if discontinuity {
			pkt[5] = 0x80
		}
	}

	return pkt
}

func continuityCounter(pkt []byte) uint8 {
	return pkt[3] & 0x0F
}

func TestTsContinuityFixer_SkipReservedAdaptationFieldControlZero(t *testing.T) {
	fixer := NewTsContinuityFixer()
	pid := uint16(256)

	pkt1 := makeTSPacket(pid, 0x1, 5, false)
	fixer.FixSegment(pkt1)

	// Reserved/invalid adaptation_field_control=0 must be ignored and never patched.
	pkt2 := makeTSPacket(pid, 0x0, 2, false)
	fixer.FixSegment(pkt2)
	if got := continuityCounter(pkt2); got != 2 {
		t.Fatalf("reserved afc=0 packet should not be patched: got CC=%d want=2", got)
	}

	pkt3 := makeTSPacket(pid, 0x1, 9, false)
	result := fixer.FixSegment(pkt3)
	if result.Patched != 1 {
		t.Fatalf("unexpected patch count: got=%d want=1", result.Patched)
	}
	if got := continuityCounter(pkt3); got != 6 {
		t.Fatalf("unexpected CC after afc=0 skip: got=%d want=6", got)
	}
}

func TestTsContinuityFixer_RespectsDiscontinuityIndicator(t *testing.T) {
	fixer := NewTsContinuityFixer()
	pid := uint16(301)

	pkt1 := makeTSPacket(pid, 0x1, 7, false)
	fixer.FixSegment(pkt1)

	// Packet with discontinuity flag should become new baseline without patching.
	pkt2 := makeTSPacket(pid, 0x3, 3, true)
	result := fixer.FixSegment(pkt2)
	if result.Patched != 0 {
		t.Fatalf("discontinuity packet should not be patched: got=%d", result.Patched)
	}
	if got := continuityCounter(pkt2); got != 3 {
		t.Fatalf("discontinuity packet CC changed unexpectedly: got=%d want=3", got)
	}

	pkt3 := makeTSPacket(pid, 0x1, 9, false)
	result = fixer.FixSegment(pkt3)
	if result.Patched != 1 {
		t.Fatalf("expected one patch after discontinuity baseline: got=%d", result.Patched)
	}
	if got := continuityCounter(pkt3); got != 4 {
		t.Fatalf("unexpected CC after discontinuity baseline: got=%d want=4", got)
	}
}

func buildTSSegment(packetCount int, pidCount int) []byte {
	if pidCount <= 0 {
		pidCount = 1
	}
	segment := make([]byte, 0, packetCount*testTsPacketSize)

	for i := 0; i < packetCount; i++ {
		pid := uint16(100 + (i % pidCount))
		cc := uint8(i % 16)
		segment = append(segment, makeTSPacket(pid, 0x1, cc, false)...)
	}

	return segment
}

func BenchmarkTsContinuityFixer_FixSegment_200Packets(b *testing.B) {
	fixer := NewTsContinuityFixer()
	base := buildTSSegment(200, 16)
	work := make([]byte, len(base))

	b.ReportAllocs()
	b.SetBytes(int64(len(base)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		copy(work, base)
		_ = fixer.FixSegment(work)
	}
}

func BenchmarkTsContinuityFixer_FixSegment_1200Packets(b *testing.B) {
	fixer := NewTsContinuityFixer()
	base := buildTSSegment(1200, 64)
	work := make([]byte, len(base))

	b.ReportAllocs()
	b.SetBytes(int64(len(base)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		copy(work, base)
		_ = fixer.FixSegment(work)
	}
}

func BenchmarkTsContinuityFixer_FixSegment_Parallel(b *testing.B) {
	base := buildTSSegment(600, 128)
	b.ReportAllocs()
	b.SetBytes(int64(len(base)))
	b.RunParallel(func(pb *testing.PB) {
		fixer := NewTsContinuityFixer()
		work := make([]byte, len(base))
		for pb.Next() {
			copy(work, base)
			_ = fixer.FixSegment(work)
		}
	})
}
