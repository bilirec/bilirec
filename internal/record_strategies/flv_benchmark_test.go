package record_strategies

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/pkg/benchreport"
	"github.com/bilirec/bilirec/pkg/flv"
)

func makeFLVStreamChunk() []byte {
	audioTag := flv.NewTagBytes(flv.TagTypeAudio, []byte{0xaf, 0x01, 0x22})
	videoTag := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x27, 0x01, 0x00, 0x00, 0x00})
	chunk := make([]byte, 0, flv.PrevTagSizeBytes+len(audioTag)+len(videoTag))
	chunk = append(chunk, 0, 0, 0, 0)
	chunk = append(chunk, audioTag...)
	chunk = append(chunk, videoTag...)
	return chunk
}

func BenchmarkFlvStrategy_PipelineWriterThroughput(b *testing.B) {
	ensureBenchmarkConfig()
	ctx := context.Background()
	strategy := NewFlvStrategy(10000)
	outputPath := filepath.Join(b.TempDir(), "bench.flv")
	pipe, err := strategy.BuildPipeline(ctx, outputPath, &RotationState{Data: map[string][]byte{}})
	if err != nil {
		b.Fatalf("BuildPipeline failed: %v", err)
	}
	if err := pipe.Open(ctx); err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer pipe.Close()

	// Prime FLV session header once.
	if _, err := pipe.Process(ctx, flv.FlvHeader); err != nil {
		b.Fatalf("header process failed: %v", err)
	}

	chunk := makeFLVStreamChunk()
	mon := benchreport.Start(b, int64(len(chunk)))
	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; i < b.N; i++ {
		if _, err := pipe.Process(ctx, chunk); err != nil {
			b.Fatalf("Process failed: %v", err)
		}
		mon.SamplePeriodically(i)
	}
}
