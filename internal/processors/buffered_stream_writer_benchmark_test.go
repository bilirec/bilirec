package processors_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
)

func benchmarkWriterPipeline(b *testing.B, chunkSize int, chanBuf int, flushPeriod time.Duration) {
	ctx := context.Background()
	writerInfo := processors.NewBufferedStreamWriter(
		filepath.Join(b.TempDir(), "bench_writer.out"),
		processors.WithBufferSize(1024*1024),
		processors.WithChanBufferSize(chanBuf),
		processors.WithFlushPeriod(flushPeriod),
		processors.WithBytesPool(pool.NewBucketedBytesPool(512*1024)),
	)
	pipe := pipeline.New(writerInfo)
	if err := pipe.Open(ctx); err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer pipe.Close()

	chunk := make([]byte, chunkSize)
	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pipe.Process(ctx, chunk); err != nil {
			b.Fatalf("Process failed: %v", err)
		}
	}
}

func BenchmarkBufferedStreamWriter_CopyPath(b *testing.B) {
	b.Run("pool_hit_128KB", func(b *testing.B) {
		benchmarkWriterPipeline(b, 128*1024, 64, 100*time.Millisecond)
	})
	b.Run("pool_border_512KB", func(b *testing.B) {
		benchmarkWriterPipeline(b, 512*1024, 64, 100*time.Millisecond)
	})
	b.Run("pool_oversized_3MB", func(b *testing.B) {
		benchmarkWriterPipeline(b, 3*1024*1024, 16, 100*time.Millisecond)
	})
}

func BenchmarkBufferedStreamWriter_BurstBackpressure(b *testing.B) {
	b.Run("burst_small_channel", func(b *testing.B) {
		benchmarkWriterPipeline(b, 256*1024, 2, 500*time.Millisecond)
	})
	b.Run("burst_large_channel", func(b *testing.B) {
		benchmarkWriterPipeline(b, 256*1024, 128, 500*time.Millisecond)
	})
}

func BenchmarkBufferedStreamWriter_FLVLikeDistribution(b *testing.B) {
	b.Run("small_high_frequency", func(b *testing.B) {
		benchmarkWriterPipeline(b, 16*1024, 64, 100*time.Millisecond)
	})
	b.Run("mixed_large", func(b *testing.B) {
		benchmarkWriterPipeline(b, 640*1024, 32, 100*time.Millisecond)
	})
}
