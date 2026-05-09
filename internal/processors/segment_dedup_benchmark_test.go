package processors

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
)

func makePayload(size int, seed byte) []byte {
	p := make([]byte, size)
	for i := range p {
		p[i] = byte((i*31 + int(seed)) & 0xFF)
	}
	return p
}

func benchmarkSegmentDedup(b *testing.B, payloadSize int, duplicate bool) {
	proc := &SegmentDedupProcessor{}
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	entry := logrus.NewEntry(logger)

	if err := proc.Open(context.Background(), entry); err != nil {
		b.Fatalf("open processor: %v", err)
	}
	b.Cleanup(func() {
		_ = proc.Close()
	})

	a := makePayload(payloadSize, 0x11)
	bData := makePayload(payloadSize, 0x77)

	b.ReportAllocs()
	b.SetBytes(int64(payloadSize))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var payload []byte
		if duplicate {
			payload = a
		} else if i&1 == 0 {
			payload = a
		} else {
			payload = bData
		}

		if _, err := proc.Process(context.Background(), entry, payload); err != nil {
			b.Fatalf("process failed: %v", err)
		}
	}
}

func BenchmarkSegmentDedup_Unique64KB(b *testing.B) {
	benchmarkSegmentDedup(b, 64*1024, false)
}

func BenchmarkSegmentDedup_Duplicate64KB(b *testing.B) {
	benchmarkSegmentDedup(b, 64*1024, true)
}

func BenchmarkSegmentDedup_Unique1MB(b *testing.B) {
	benchmarkSegmentDedup(b, 1024*1024, false)
}

func BenchmarkSegmentDedup_Duplicate1MB(b *testing.B) {
	benchmarkSegmentDedup(b, 1024*1024, true)
}

// BenchmarkSegmentDedup_VaryingSize simulates a realistic HLS stream where each
// segment has a slightly different length (the common production case).
// Phase 1 fingerprint should reject the match on length alone, skipping SHA-256.
func BenchmarkSegmentDedup_VaryingSize(b *testing.B) {
	proc := &SegmentDedupProcessor{}
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	entry := logrus.NewEntry(logger)

	if err := proc.Open(context.Background(), entry); err != nil {
		b.Fatalf("open processor: %v", err)
	}
	b.Cleanup(func() { _ = proc.Close() })

	// Pre-build 8 segments of slightly different sizes (±512 B around 256 KB)
	const base = 256 * 1024
	segs := make([][]byte, 8)
	for i := range segs {
		segs[i] = makePayload(base+i*512, byte(i))
	}

	b.ReportAllocs()
	b.SetBytes(base)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		payload := segs[i%len(segs)]
		if _, err := proc.Process(context.Background(), entry, payload); err != nil {
			b.Fatalf("process failed: %v", err)
		}
	}
}
