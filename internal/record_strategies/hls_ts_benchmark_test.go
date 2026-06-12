package record_strategies

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/pkg/benchreport"
)

func makeBenchmarkTSPacket(pid uint16, cc byte) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = byte((pid >> 8) & 0x1F)
	pkt[2] = byte(pid & 0xFF)
	pkt[3] = 0x10 | (cc & 0x0F)
	return pkt
}

func makeBenchmarkTSSegment(packetCount int) []byte {
	if packetCount < 1 {
		packetCount = 1
	}
	out := make([]byte, 0, packetCount*188)
	for i := 0; i < packetCount; i++ {
		out = append(out, makeBenchmarkTSPacket(uint16(256+i%32), byte(i%16))...)
	}
	return out
}

func BenchmarkHlsTsStrategy_PipelineThroughput(b *testing.B) {
	ensureBenchmarkConfig()
	ctx := context.Background()
	strategy := NewHlsTsStrategy(10000)
	outputPath := filepath.Join(b.TempDir(), "bench.ts")
	pipe, err := strategy.BuildPipeline(ctx, outputPath, &RotationState{Data: map[string][]byte{}})
	if err != nil {
		b.Fatalf("BuildPipeline failed: %v", err)
	}
	if err := pipe.Open(ctx); err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer pipe.Close()

	segment := makeBenchmarkTSSegment(700) // ~131KB
	mon := benchreport.Start(b, int64(len(segment)))
	b.ReportAllocs()
	b.SetBytes(int64(len(segment)))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; i < b.N; i++ {
		if _, err := pipe.Process(ctx, segment); err != nil {
			b.Fatalf("Process failed: %v", err)
		}
		mon.SamplePeriodically(i)
	}
}
