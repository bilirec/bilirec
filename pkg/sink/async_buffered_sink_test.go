package sink

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingTransport struct {
	mu     sync.Mutex
	closed atomic.Bool
	batch  bytes.Buffer
}

func (t *recordingTransport) Consume(p []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = t.batch.Write(p)
	return nil
}

func (t *recordingTransport) Close(context.Context) error {
	t.closed.Store(true)
	return nil
}

func (t *recordingTransport) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.batch.String()
}

type blockingTransport struct {
	blockCh chan struct{}
}

func (t *blockingTransport) Consume(_ []byte) error {
	<-t.blockCh
	return nil
}

func (t *blockingTransport) Close(context.Context) error {
	return nil
}

func TestNewAsyncBufferedSinkRejectsNilTransport(t *testing.T) {
	_, err := NewAsyncBufferedSink(nil, Options{})
	if !errors.Is(err, ErrNilTransport) {
		t.Fatalf("err = %v, want ErrNilTransport", err)
	}
}

func TestDropPolicyDropsWhenFullWithoutBlocking(t *testing.T) {
	block := make(chan struct{})
	tr := &blockingTransport{blockCh: block}
	s, err := NewAsyncBufferedSink(tr, Options{
		BufferSize:    1,
		BatchBytes:    1,
		FlushInterval: time.Hour,
		Overflow:      OverflowDrop,
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	defer func() {
		close(block)
		_ = s.Stop(noCancelCtx{})
	}()

	if _, err := s.Write([]byte("a")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := s.Write([]byte("b")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.Write([]byte("c"))
	}()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("drop policy write blocked")
	}
}

func TestBlockPolicyBackpressuresWhenFull(t *testing.T) {
	block := make(chan struct{})
	tr := &blockingTransport{blockCh: block}
	s, err := NewAsyncBufferedSink(tr, Options{
		BufferSize:    1,
		BatchBytes:    1,
		FlushInterval: time.Hour,
		Overflow:      OverflowBlock,
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}

	if _, err := s.Write([]byte("a")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := s.Write([]byte("b")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.Write([]byte("c"))
	}()

	select {
	case <-done:
		t.Fatal("block policy should backpressure when full")
	case <-time.After(150 * time.Millisecond):
	}

	close(block)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked write did not resume")
	}
	_ = s.Stop(noCancelCtx{})
}

func TestSyncFlushesPendingData(t *testing.T) {
	tr := &recordingTransport{}
	s, err := NewAsyncBufferedSink(tr, Options{
		BufferSize:    8,
		BatchBytes:    1024,
		FlushInterval: time.Hour,
		Overflow:      OverflowBlock,
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	defer func() { _ = s.Stop(noCancelCtx{}) }()

	if _, err := s.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := s.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := tr.String(); got != "hello\n" {
		t.Fatalf("transport got %q, want %q", got, "hello\n")
	}
}

func TestStopDrainsPendingDataAndClosesTransport(t *testing.T) {
	tr := &recordingTransport{}
	s, err := NewAsyncBufferedSink(tr, Options{
		BufferSize:    8,
		BatchBytes:    1024,
		FlushInterval: time.Hour,
		Overflow:      OverflowBlock,
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}

	if _, err := s.Write([]byte("a\n")); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if _, err := s.Write([]byte("b\n")); err != nil {
		t.Fatalf("write b: %v", err)
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := tr.String(); got != "a\nb\n" {
		t.Fatalf("transport got %q, want %q", got, "a\nb\n")
	}
	if !tr.closed.Load() {
		t.Fatal("transport close was not called")
	}
}

type noCancelCtx struct{}

func (noCancelCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (noCancelCtx) Done() <-chan struct{}       { return nil }
func (noCancelCtx) Err() error                  { return nil }
func (noCancelCtx) Value(any) any               { return nil }
