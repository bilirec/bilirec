package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildInsertURL(t *testing.T) {
	got := buildInsertURL("http://127.0.0.1:9428", "app,logger")
	if !strings.Contains(got, "/insert/jsonline") {
		t.Fatalf("missing path: %q", got)
	}
	if !strings.Contains(got, "_stream_fields=app%2Clogger") && !strings.Contains(got, "_stream_fields=app,logger") {
		t.Fatalf("missing stream fields: %q", got)
	}
}

func TestAttachJSONLinePostsNDJSON(t *testing.T) {
	var mu sync.Mutex
	var bodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/insert/jsonline") {
			t.Errorf("path: %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/stream+json" {
			t.Errorf("content-type: %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	color := false
	Init(Options{Output: io.Discard, Color: &color})

	sink := AttachJSONLine(JSONLineOptions{
		URL:           srv.URL,
		StreamFields:  "app,logger",
		App:           "bilirec",
		Instance:      "test-host",
		FlushInterval: 50 * time.Millisecond,
		BufferSize:    64,
	})
	t.Cleanup(func() {
		_ = sink.Stop(noCancelCtx{})
		remoteCore = nil
		remoteSink = nil
		rebuildRoot()
	})

	Named("recorder").With("room", 42).Info("錄製開始")
	Sync()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	all := strings.Join(bodies, "")
	mu.Unlock()
	if all == "" {
		t.Fatal("no request body received")
	}

	for _, line := range strings.Split(strings.TrimSpace(all), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("invalid json line %q: %v", line, err)
		}
		if row["_msg"] != "錄製開始" {
			t.Fatalf("_msg: %v", row["_msg"])
		}
		if row["app"] != "bilirec" {
			t.Fatalf("app: %v", row["app"])
		}
		if row["logger"] != "recorder" {
			t.Fatalf("logger: %v", row["logger"])
		}
		if _, ok := row["_time"]; !ok {
			t.Fatalf("missing _time in %v", row)
		}
	}
}

func TestJSONLineSinkDropOnFull(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(block)

	s := newJSONLineSink(JSONLineOptions{
		URL:           srv.URL,
		BufferSize:    1,
		FlushInterval: time.Hour,
		BatchBytes:    1 << 20,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_, _ = s.Write([]byte(`{"_msg":"x"}` + "\n"))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on full buffer")
	}
}

func TestJSONLineSinkSyncFlushes(t *testing.T) {
	var mu sync.Mutex
	var got string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = string(body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newJSONLineSink(JSONLineOptions{
		URL:           srv.URL,
		FlushInterval: time.Hour,
		BatchBytes:    1 << 20,
		BufferSize:    8,
	})
	t.Cleanup(func() { _ = s.Stop(noCancelCtx{}) })

	_, _ = s.Write([]byte(`{"_msg":"flush-me"}` + "\n"))
	_ = s.Sync()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		body := got
		mu.Unlock()
		if strings.Contains(body, "flush-me") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sync did not flush, got %q", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPrettyOutputUnchangedWithRemote(t *testing.T) {
	buf := &bytes.Buffer{}
	color := false
	Init(Options{Output: buf, Color: &color})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := AttachJSONLine(JSONLineOptions{
		URL:           srv.URL,
		FlushInterval: 50 * time.Millisecond,
	})
	t.Cleanup(func() {
		_ = sink.Stop(noCancelCtx{})
		remoteCore = nil
		remoteSink = nil
		rebuildRoot()
	})

	Named("recorder").Info("hello")
	Sync()

	got := buf.String()
	if !strings.Contains(got, "  INFO   recorder  hello") {
		t.Fatalf("pretty output changed: %q", got)
	}
	if strings.Contains(got, "{") {
		t.Fatalf("pretty output should not be json: %q", got)
	}
}

type noCancelCtx struct{}

func (noCancelCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (noCancelCtx) Done() <-chan struct{}      { return nil }
func (noCancelCtx) Err() error                 { return nil }
func (noCancelCtx) Value(any) any              { return nil }
