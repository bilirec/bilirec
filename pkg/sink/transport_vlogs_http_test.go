package sink

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestVLogsTransportRetriesTransientFailures(t *testing.T) {
	var attempts atomic.Int32
	var failed atomic.Int32
	var retries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := NewVLogsHTTPTransport(VLogsHTTPTransportOptions{
		URL:      srv.URL,
		RetryMax: 2,
		OnFailed: func() { failed.Add(1) },
		OnRetry:  func() { retries.Add(1) },
	})

	err := tr.Consume([]byte(`{"_msg":"retry-me"}` + "\n"))
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
	if got := failed.Load(); got != 2 {
		t.Fatalf("failed requests = %d, want 2", got)
	}
	if got := retries.Load(); got != 2 {
		t.Fatalf("retries = %d, want 2", got)
	}
}

func TestVLogsTransportPostsStreamJSON(t *testing.T) {
	var (
		gotMethod     string
		gotPath       string
		gotContType   string
		gotAccountID  string
		gotProjectID  string
		gotBody       string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContType = r.Header.Get("Content-Type")
		gotAccountID = r.Header.Get("AccountID")
		gotProjectID = r.Header.Get("ProjectID")
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := NewVLogsHTTPTransport(VLogsHTTPTransportOptions{
		URL:       srv.URL + "/insert/jsonline",
		AccountID: "acc-1",
		ProjectID: "proj-1",
		RetryMax:  0,
	})

	payload := `{"_msg":"hello"}` + "\n"
	if err := tr.Consume([]byte(payload)); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/insert/jsonline" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotContType != "application/stream+json" {
		t.Fatalf("content-type = %q", gotContType)
	}
	if gotAccountID != "acc-1" {
		t.Fatalf("AccountID header = %q", gotAccountID)
	}
	if gotProjectID != "proj-1" {
		t.Fatalf("ProjectID header = %q", gotProjectID)
	}
	if gotBody != payload {
		t.Fatalf("body = %q, want %q", gotBody, payload)
	}
}

func TestVLogsTransportNoRetryOnClientError(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	tr := NewVLogsHTTPTransport(VLogsHTTPTransportOptions{
		URL:      srv.URL,
		RetryMax: 2,
	})

	err := tr.Consume([]byte(`{"_msg":"bad"}` + "\n"))
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 (no retry on 4xx)", got)
	}
}

func TestVLogsTransportNoRetryWhenRetryMaxZero(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tr := NewVLogsHTTPTransport(VLogsHTTPTransportOptions{
		URL:      srv.URL,
		RetryMax: 0,
	})

	err := tr.Consume([]byte(`{"_msg":"no-retry"}` + "\n"))
	if err == nil {
		t.Fatal("expected error on 503")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 (RetryMax=0 disables retry)", got)
	}
}

func TestVLogsTransportNewRequestError(t *testing.T) {
	tr := NewVLogsHTTPTransport(VLogsHTTPTransportOptions{
		URL: "://invalid-url",
	})
	err := tr.Consume([]byte(`{"_msg":"bad-url"}` + "\n"))
	if err == nil {
		t.Fatal("expected error for invalid request URL")
	}
}

func TestVLogsTransportConsumeEmptyBatch(t *testing.T) {
	tr := NewVLogsHTTPTransport(VLogsHTTPTransportOptions{
		URL: "http://127.0.0.1:1/insert/jsonline",
	})
	if err := tr.Consume(nil); err != nil {
		t.Fatalf("consume empty batch: %v", err)
	}
}
