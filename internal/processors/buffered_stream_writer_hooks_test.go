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

func TestBufferedStreamWriter_BytesWrittenOnWriteOnly(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "out.bin")

	var written int
	writerInfo := processors.NewBufferedStreamWriter(
		testFile,
		processors.WithBufferSize(64*1024),
		processors.WithChanBufferSize(1),
		processors.WithBytesPool(pool.NewBucketedBytesPool(16*1024)),
		processors.WithOnBytesWritten(func(n int) { written += n }),
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}

	chunk := make([]byte, 1024)
	if _, err := pipe.Process(ctx, chunk); err != nil {
		t.Fatalf("process: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for written == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if written != len(chunk) {
		t.Fatalf("expected %d bytes written callback, got %d", len(chunk), written)
	}

	pipe.Close()
}

func TestBufferedStreamWriter_EnqueueWaitHook(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "out.bin")

	var waits int
	writerInfo := processors.NewBufferedStreamWriter(
		testFile,
		processors.WithBufferSize(64*1024),
		processors.WithChanBufferSize(1),
		processors.WithBytesPool(pool.NewBucketedBytesPool(16*1024)),
		processors.WithOnEnqueueWait(func(ev processors.EnqueueWaitEvent) {
			waits++
			if ev.Bytes <= 0 {
				t.Errorf("unexpected enqueue wait event: %+v", ev)
			}
		}),
	)
	pipe := pipeline.New(writerInfo)

	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}

	chunk := make([]byte, 512)
	for i := 0; i < 8; i++ {
		if _, err := pipe.Process(ctx, chunk); err != nil {
			t.Fatalf("process %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for waits == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if waits == 0 {
		t.Fatal("expected enqueue wait callback when writer queue backs up")
	}

	pipe.Close()
}
