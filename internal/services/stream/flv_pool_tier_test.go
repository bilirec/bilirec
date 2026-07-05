package stream

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/benchreport"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/sirupsen/logrus"
)

func newTestChunkPools(defaultSize, highSize int) *pool.LazyDualPool[*pool.BucketedBytesPool] {
	return pool.NewLazyDualPool(
		15*time.Minute,
		func() *pool.BucketedBytesPool { return newChunkBytesPool(defaultSize, 4) },
		func() *pool.BucketedBytesPool { return newChunkBytesPool(highSize, 4) },
	)
}

func TestFlvRead_4KPayload_DefaultVsHighPool(t *testing.T) {
	initStreamTestConfig(t)

	const (
		payloadSize = 900 * 1024 // 900KB: larger than default 512KB, smaller than high 1MB
		defaultSize = 512 * 1024
		highSize    = 1024 * 1024
	)

	newSvc := func() *Service {
		return &Service{
			chunkPools:         newTestChunkPools(defaultSize, highSize),
			chanBufferSize:     64,
			highChanBufferSize: 64,
		}
	}

	run := func(svc *Service, qn int) (chunks int, totalBytes int) {
		chunkPool, release := svc.AcquireChunkPool(qn)
		defer release()

		ch := make(chan []byte, 8)
		ctx := context.Background()
		stream := io.NopCloser(bytes.NewReader(make([]byte, payloadSize)))
		readSize := svc.readBufSizeForQn(qn)
		go svc.readFlv(ch, stream, ctx, chunkPool, readSize, func() {})

		for chunk := range ch {
			chunks++
			totalBytes += len(chunk)
			svc.putChunk(chunkPool, chunk)
		}
		return chunks, totalBytes
	}

	defaultChunks, defaultBytes := run(newSvc(), 10000)
	highChunks, highBytes := run(newSvc(), 20000)

	if defaultBytes != payloadSize {
		t.Fatalf("default pool total bytes mismatch: got %d, want %d", defaultBytes, payloadSize)
	}
	if highBytes != payloadSize {
		t.Fatalf("high pool total bytes mismatch: got %d, want %d", highBytes, payloadSize)
	}

	if defaultChunks <= 1 {
		t.Fatalf("expected default pool to split payload into multiple chunks, got %d", defaultChunks)
	}
	if highChunks != 1 {
		t.Fatalf("expected high pool to read payload in one chunk, got %d", highChunks)
	}
}

func BenchmarkFlvRead_4KPayload_DefaultVsHighPool(b *testing.B) {
	initStreamTestConfig(b)

	const (
		payloadSize = 900 * 1024
		defaultSize = 512 * 1024
		highSize    = 1024 * 1024
	)

	logrus.SetLevel(logrus.WarnLevel)
	defer logrus.SetLevel(logrus.InfoLevel)

	newSvc := func() *Service {
		return &Service{
			chunkPools:         newTestChunkPools(defaultSize, highSize),
			chanBufferSize:     64,
			highChanBufferSize: 64,
		}
	}

	runOnce := func(svc *Service, qn int) (chunks int, totalBytes int) {
		chunkPool, release := svc.AcquireChunkPool(qn)
		defer release()

		ch := make(chan []byte, 8)
		ctx := context.Background()
		stream := io.NopCloser(bytes.NewReader(make([]byte, payloadSize)))
		readSize := svc.readBufSizeForQn(qn)
		go svc.readFlv(ch, stream, ctx, chunkPool, readSize, func() {})

		for chunk := range ch {
			chunks++
			totalBytes += len(chunk)
			svc.putChunk(chunkPool, chunk)
		}
		return chunks, totalBytes
	}

	type perfResult struct {
		avgChunks float64
		usPerOp   float64
		mbPerSec  float64
		valid     bool
	}
	var normalRes perfResult
	var highRes perfResult

	b.Cleanup(func() {
		if !normalRes.valid || !highRes.valid {
			return
		}
		chunkReductionPct := (normalRes.avgChunks - highRes.avgChunks) / normalRes.avgChunks * 100.0
		latencyImprovePct := (normalRes.usPerOp - highRes.usPerOp) / normalRes.usPerOp * 100.0
		throughputImprovePct := (highRes.mbPerSec - normalRes.mbPerSec) / normalRes.mbPerSec * 100.0
		b.Logf("📊 4K pool comparison (900KB payload):")
		b.Logf("  normal(512KB): chunks/op=%.2f, us/op=%.2f, throughput=%.2f MB/s", normalRes.avgChunks, normalRes.usPerOp, normalRes.mbPerSec)
		b.Logf("  high(1MB):     chunks/op=%.2f, us/op=%.2f, throughput=%.2f MB/s", highRes.avgChunks, highRes.usPerOp, highRes.mbPerSec)
		b.Logf("  delta: chunk reduction=%.2f%%, latency improvement=%.2f%%, throughput improvement=%.2f%%", chunkReductionPct, latencyImprovePct, throughputImprovePct)
	})

	b.Run("normal_pool_512KB", func(b *testing.B) {
		svc := newSvc()
		mon := benchreport.Start(b, payloadSize)
		b.ReportAllocs()
		b.SetBytes(payloadSize)
		b.ResetTimer()
		start := time.Now()
		mon.MarkTimerStart()
		var totalChunks int64
		for i := 0; i < b.N; i++ {
			chunks, total := runOnce(svc, 10000)
			if total != payloadSize {
				b.Fatalf("unexpected total bytes: got %d want %d", total, payloadSize)
			}
			if chunks <= 1 {
				b.Fatalf("expected chunk split under normal pool, got chunks=%d", chunks)
			}
			totalChunks += int64(chunks)
			mon.SamplePeriodically(i)
		}
		elapsed := time.Since(start)
		avgChunks := float64(totalChunks) / float64(b.N)
		b.ReportMetric(avgChunks, "chunks/op")
		b.ReportMetric((avgChunks-1.0)*100.0, "chunk_overhead_pct")
		normalRes = perfResult{
			avgChunks: avgChunks,
			usPerOp:   float64(elapsed.Microseconds()) / float64(b.N),
			mbPerSec:  float64(payloadSize*b.N) / elapsed.Seconds() / (1024 * 1024),
			valid:     true,
		}
		b.Cleanup(func() {
			b.Logf("📊 normal pool summary: avgChunks=%.2f (payload=900KB, pool=512KB)", avgChunks)
		})
	})

	b.Run("high_pool_1MB", func(b *testing.B) {
		svc := newSvc()
		mon := benchreport.Start(b, payloadSize)
		b.ReportAllocs()
		b.SetBytes(payloadSize)
		b.ResetTimer()
		start := time.Now()
		mon.MarkTimerStart()
		var totalChunks int64
		for i := 0; i < b.N; i++ {
			chunks, total := runOnce(svc, 20000)
			if total != payloadSize {
				b.Fatalf("unexpected total bytes: got %d want %d", total, payloadSize)
			}
			if chunks != 1 {
				b.Fatalf("expected single chunk under high pool, got chunks=%d", chunks)
			}
			totalChunks += int64(chunks)
			mon.SamplePeriodically(i)
		}
		elapsed := time.Since(start)
		avgChunks := float64(totalChunks) / float64(b.N)
		b.ReportMetric(avgChunks, "chunks/op")
		b.ReportMetric((avgChunks-1.0)*100.0, "chunk_overhead_pct")
		highRes = perfResult{
			avgChunks: avgChunks,
			usPerOp:   float64(elapsed.Microseconds()) / float64(b.N),
			mbPerSec:  float64(payloadSize*b.N) / elapsed.Seconds() / (1024 * 1024),
			valid:     true,
		}
		b.Cleanup(func() {
			b.Logf("📊 high pool summary: avgChunks=%.2f (payload=900KB, pool=1MB)", avgChunks)
		})
	})
}
