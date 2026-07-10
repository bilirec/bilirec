package hls

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func TestSegmentPrefetcher_WaitCancelReleasesBody(t *testing.T) {
	var released atomic.Int64
	blockRead := make(chan struct{})
	var readStarted sync.Once
	readStartedCh := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("segment-body"))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	resolver, err := NewURLResolver(server.URL + "/playlist.m3u8")
	if err != nil {
		t.Fatalf("NewURLResolver: %v", err)
	}

	p := NewSegmentPrefetcher(
		ctx,
		resty.New(),
		resolver,
		1,
		0,
		1,
		func(resp *resty.Response) ([]byte, error) {
			readStarted.Do(func() { close(readStartedCh) })
			<-blockRead
			return []byte("owned-buffer"), nil
		},
		func(buf []byte) {
			if string(buf) == "owned-buffer" {
				released.Add(1)
			}
		},
	)

	done := make(chan error, 1)
	go func() {
		_, waitErr := p.Wait(1, "seg-1.ts")
		done <- waitErr
	}()

	select {
	case <-readStartedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("readBody did not start")
	}

	cancel()
	select {
	case waitErr := <-done:
		if waitErr == nil {
			t.Fatal("Wait should fail after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after cancel")
	}

	close(blockRead)
	waitReleased(t, &released)
}

func TestSegmentPrefetcher_AbandonReleasesCompletedBody(t *testing.T) {
	var released atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("segment-body"))
	}))
	defer server.Close()

	resolver, err := NewURLResolver(server.URL + "/playlist.m3u8")
	if err != nil {
		t.Fatalf("NewURLResolver: %v", err)
	}

	p := NewSegmentPrefetcher(
		context.Background(),
		resty.New(),
		resolver,
		1,
		0,
		1,
		func(resp *resty.Response) ([]byte, error) {
			return []byte("owned-buffer"), nil
		},
		func(buf []byte) {
			if string(buf) == "owned-buffer" {
				released.Add(1)
			}
		},
	)

	p.Start(8, "seg-8.ts")
	// Allow the prefetch goroutine to finish and park the result in the buffered channel.
	time.Sleep(100 * time.Millisecond)
	p.Abandon()
	waitReleased(t, &released)
}

func waitReleased(t *testing.T, released *atomic.Int64) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for released.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("expected body to be released, got %d", released.Load())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
