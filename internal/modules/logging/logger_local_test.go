package logging

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
)

func resetCoresForTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		logger.ClearLocalCore()
		logger.ClearRemoteCore()
	})
}

func TestLocalPrettyFormat(t *testing.T) {
	color := false
	logger.Init(logger.Options{Output: io.Discard, Color: &color})
	resetCoresForTest(t)

	dir := t.TempDir()
	logPath := filepath.Join(dir, "local.log")

	// Simulate what wireLocalLogger builds for format=pretty.
	fileWriter := newTestLumberjack(t, logPath)
	bufferedSink := newTestBufferedSink(t, fileWriter, 50*time.Millisecond)

	localCore := buildLocalCore("pretty", bufferedSink)
	logger.SetLocalCore(localCore)

	logger.Named("local").Info("hello-local")
	logger.Sync()
	_ = bufferedSink.Stop(noCancelCtx{})

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	out := string(body)
	if !strings.Contains(out, "INFO") || !strings.Contains(out, "hello-local") {
		t.Fatalf("unexpected local pretty output: %q", out)
	}
	if strings.Contains(out, `"message":`) || strings.Contains(out, `"timestamp":`) {
		t.Fatalf("pretty local output must not be jsonline: %q", out)
	}
}

func TestLocalJsonLineFormat(t *testing.T) {
	color := false
	logger.Init(logger.Options{Output: io.Discard, Color: &color})
	resetCoresForTest(t)

	dir := t.TempDir()
	logPath := filepath.Join(dir, "local.log")

	fileWriter := newTestLumberjack(t, logPath)
	bufferedSink := newTestBufferedSink(t, fileWriter, 50*time.Millisecond)

	localCore := buildLocalCore("jsonline", bufferedSink)
	logger.SetLocalCore(localCore)

	logger.Named("local").Info("hello-json")
	logger.Sync()
	_ = bufferedSink.Stop(noCancelCtx{})

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	out := string(body)
	if !strings.Contains(out, `"message":"hello-json"`) {
		t.Fatalf("local jsonline output expected, got: %q", out)
	}
}

func TestRemoteJSONUnaffectedByLocalFormat(t *testing.T) {
	color := false
	logger.Init(logger.Options{Output: io.Discard, Color: &color})
	resetCoresForTest(t)

	dir := t.TempDir()
	fileWriter := newTestLumberjack(t, filepath.Join(dir, "local.log"))
	bufferedSink := newTestBufferedSink(t, fileWriter, 50*time.Millisecond)
	localCore := buildLocalCore("pretty", bufferedSink)
	logger.SetLocalCore(localCore)
	t.Cleanup(func() {
		_ = bufferedSink.Stop(noCancelCtx{})
	})

	var mu sync.Mutex
	var payload string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		payload += string(body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := newTestVLogsTransport(t, srv.URL, 50*time.Millisecond)
	remoteSink := newTestBufferedSink(t, tr, 50*time.Millisecond)
	remoteCore := buildRemoteCore(remoteSink, "test-host")
	logger.SetRemoteCore(remoteCore)
	t.Cleanup(func() {
		_ = remoteSink.Stop(noCancelCtx{})
	})

	logger.Named("remote").Info("remote-json-check")
	logger.Sync()
	time.Sleep(120 * time.Millisecond)

	mu.Lock()
	got := payload
	mu.Unlock()

	if !strings.Contains(got, `"_msg":"remote-json-check"`) {
		t.Fatalf("remote payload must stay jsonline, got: %q", got)
	}
	if strings.Contains(got, "  INFO   remote  remote-json-check") {
		t.Fatalf("remote payload unexpectedly became pretty text: %q", got)
	}
}
