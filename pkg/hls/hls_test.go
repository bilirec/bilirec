package hls

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func TestIsRetryableFetchErr(t *testing.T) {
	t.Parallel()

	restyDeadlineErr := fmt.Errorf("拉取分片失败：%w", context.DeadlineExceeded)

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "canceled", err: context.Canceled, want: false},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "wrapped deadline exceeded", err: restyDeadlineErr, want: true},
		{name: "resty client timeout message", err: errors.New("context deadline exceeded (Client.Timeout or context cancellation while reading body)"), want: true},
		{name: "timeout net error", err: &net.DNSError{IsTimeout: true}, want: true},
		{name: "connection reset", err: errors.New("read tcp: connection reset by peer"), want: true},
		{name: "permanent", err: errors.New("certificate signed by unknown authority"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRetryableFetchErr(tt.err); got != tt.want {
				t.Fatalf("IsRetryableFetchErr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsRetryableSegmentFetchErr(t *testing.T) {
	t.Parallel()
	if !IsRetryableSegmentFetchErr(context.DeadlineExceeded) {
		t.Fatal("IsRetryableSegmentFetchErr should delegate to IsRetryableFetchErr")
	}
}

func TestFetchSegmentWithRetry_NetworkErrorRetries(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	client := resty.New().SetTimeout(50 * time.Millisecond)
	ctx := t.Context()

	_, err := FetchSegmentWithRetry(ctx, client, server.URL, 3, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("unexpected attempt count: got=%d want=3", got)
	}
}

func TestFetchSegmentWithRetry_TimeoutThenSuccess(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			time.Sleep(200 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("segment-bytes"))
	}))
	defer server.Close()

	client := resty.New().SetTimeout(50 * time.Millisecond)
	ctx := t.Context()

	data, err := FetchSegmentWithRetry(ctx, client, server.URL, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("FetchSegmentWithRetry returned error: %v", err)
	}
	if string(data) != "segment-bytes" {
		t.Fatalf("unexpected data: %q", string(data))
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("unexpected attempt count: got=%d want=2", got)
	}
}

func TestFetchSegmentWithRetry_LargeSegmentWithContentLength(t *testing.T) {
	const segmentSize = 1024 * 1024
	payload := make([]byte, segmentSize)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", segmentSize))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client := resty.New()
	ctx := t.Context()

	data, err := FetchSegmentWithRetry(ctx, client, server.URL, 1, 0)
	if err != nil {
		t.Fatalf("FetchSegmentWithRetry returned error: %v", err)
	}
	if len(data) != segmentSize {
		t.Fatalf("unexpected data length: got=%d want=%d", len(data), segmentSize)
	}
	if data[0] != payload[0] || data[segmentSize-1] != payload[segmentSize-1] {
		t.Fatal("segment payload mismatch at boundaries")
	}
}

func TestFetchSegmentWithRetry_ChunkedWithoutContentLength(t *testing.T) {
	payload := []byte("chunked-segment-payload")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Del("Content-Length")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client := resty.New()
	ctx := t.Context()

	data, err := FetchSegmentWithRetry(ctx, client, server.URL, 1, 0)
	if err != nil {
		t.Fatalf("FetchSegmentWithRetry returned error: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("unexpected data: %q", string(data))
	}
}

func TestFetchSegmentWithRetry_StatusRetryThenSuccess(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("segment-after-retry"))
	}))
	defer server.Close()

	client := resty.New()
	ctx := t.Context()

	data, err := FetchSegmentWithRetry(ctx, client, server.URL, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("FetchSegmentWithRetry returned error: %v", err)
	}
	if string(data) != "segment-after-retry" {
		t.Fatalf("unexpected data: %q", string(data))
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("unexpected attempt count: got=%d want=3", got)
	}
}
