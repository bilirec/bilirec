package processors

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/pkg/rw"
)

// panickingSink panics on every Write, standing in for an unexpected bug in
// the underlying writer that surfaces inside the write goroutine rather than
// returning an error.
type panickingSink struct {
	calls int
}

func (p *panickingSink) Write(b []byte) (int, error) {
	p.calls++
	panic("sink boom")
}

// newPanicTestWriter mirrors newFatalTestWriter but disables deferred file
// creation so the file and writer exist before any chunk arrives.
func newPanicTestWriter(t *testing.T, path string) *BufferedStreamWriterProcessor {
	t.Helper()
	w := &BufferedStreamWriterProcessor{
		path:           path,
		bufferSize:     512,
		chanBufferSize: 4,
		bytesPool:      pool.NewBucketedBytesPool(4096),
	}
	w.locker = &noOpLocker{}
	return w
}

func TestBufferedStreamWriter_PanicInWriterGoroutineBecomesFatal(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "out.flv")

	w := newPanicTestWriter(t, testFile)
	ctx := context.Background()
	if err := w.Open(ctx, logger.Nop()); err != nil {
		t.Fatalf("Open: %v", err)
	}

	sink := &panickingSink{}
	// Open already created the real file handle (deferred creation is off);
	// reuse it instead of opening a second handle so Windows cleanup can
	// still remove the temp dir.
	w.writer = rw.NewFlushLockedBufferedWriter(sink, 8, &noOpLocker{})

	// A chunk larger than the 8-byte buffer forces a real Write (and thus a
	// panic) inside the write goroutine.
	_, err := w.Process(ctx, logger.Nop(), make([]byte, 64))
	if err != nil {
		t.Fatalf("Process while writer still alive should succeed: %v", err)
	}

	// The panic is recovered by RecoverPanic, converted to fatal via setFatal,
	// and drainAfterPanic unblocks the channel. Wait for the fatal to land.
	deadline := time.Now().Add(5 * time.Second)
	for w.fatalErr() == nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for fatal error after writer panic")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(w.fatalErr().Error(), "panic") {
		t.Fatalf("fatalErr should mention panic: %v", w.fatalErr())
	}

	// Subsequent Process calls fail fast instead of blocking.
	fastCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = w.Process(fastCtx, logger.Nop(), []byte{'a'})
	if err == nil {
		t.Fatal("Process after panic should return the fatal error")
	}

	// Close must return even though the writer goroutine already died.
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBufferedStreamWriter_PanicDrainUnblocksProducer(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "out.flv")

	w := newPanicTestWriter(t, testFile)
	ctx := context.Background()
	if err := w.Open(ctx, logger.Nop()); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Tiny channel: without drainAfterPanic, a producer blocked on send would
	// deadlock forever after the writer goroutine dies.
	w.dataCh = make(chan []byte, 1)

	sink := &panickingSink{}
	// Reuse the handle Open created — a second handle on the same path leaks
	// the first one and blocks Windows temp-dir cleanup.
	w.writer = rw.NewFlushLockedBufferedWriter(sink, 8, &noOpLocker{})

	done := make(chan error, 1)
	go func() {
		var lastErr error
		for i := 0; i < 8; i++ {
			_, err := w.Process(ctx, logger.Nop(), make([]byte, 64))
			if err != nil {
				lastErr = err
				break
			}
		}
		done <- lastErr
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("producer should observe the fatal error, not deadlock")
		}
		if !strings.Contains(err.Error(), "panic") {
			t.Fatalf("producer error should carry the panic: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("producer blocked: drainAfterPanic failed to unblock dataCh")
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// The periodicIOWorker panic path is intentionally not covered: w.file is an
// atomic.Pointer[os.File], so a panicking Sync target cannot be injected
// without touching production code. The recover wiring is identical to the
// writePeriodically path covered above.
