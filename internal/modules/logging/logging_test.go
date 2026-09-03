package logging

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
	logsink "github.com/bilirec/bilirec/pkg/sink"
)

func TestShutdownSinkFlushesBeforeClearAndStop(t *testing.T) {
	color := false
	logger.Init(logger.Options{Output: io.Discard, Color: &color})
	resetCoresForTest(t)

	dir := t.TempDir()
	logPath := filepath.Join(dir, "shutdown.log")
	fileWriter := newTestLumberjack(t, logPath)
	bufferedSink := newTestBufferedSink(t, fileWriter, 50*time.Millisecond)

	localCore := buildLocalCore("pretty", bufferedSink)
	logger.SetLocalCore(localCore)

	logger.Named("shutdown").Info("before-stop")
	if err := shutdownSink(logger.ClearLocalCore, bufferedSink, context.Background()); err != nil {
		t.Fatalf("shutdownSink: %v", err)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(body), "before-stop") {
		t.Fatalf("expected flushed log before stop, got: %q", body)
	}

	_, err = bufferedSink.Write([]byte("after-stop\n"))
	if !errors.Is(err, logsink.ErrSinkStopped) {
		t.Fatalf("write after shutdown = %v, want ErrSinkStopped", err)
	}
}

func TestRemoteSinkFailureCountsOnce(t *testing.T) {
	var failedCount atomic.Int32
	tr := &failingTransport{}
	bufferedSink, err := logsink.NewAsyncBufferedSink(tr, logsink.Options{
		BufferSize:    8,
		BatchBytes:    1024,
		FlushInterval: time.Hour,
		Overflow:      logsink.OverflowDrop,
		Hooks: logsink.Hooks{
			OnFailed: func(err error) {
				failedCount.Add(1)
			},
		},
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	t.Cleanup(func() { _ = bufferedSink.Stop(noCancelCtx{}) })

	if _, err := bufferedSink.Write([]byte(`{"_msg":"fail-once"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := bufferedSink.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if got := failedCount.Load(); got != 1 {
		t.Fatalf("OnFailed calls = %d, want 1", got)
	}
	if got := tr.calls.Load(); got != 1 {
		t.Fatalf("consume calls = %d, want 1", got)
	}
}

func TestNewModuleMetricsUsesVictoriaLogsEnablement(t *testing.T) {
	if moduleMetrics := newModuleMetrics(nil, false); moduleMetrics.enabled {
		t.Fatal("metrics must be disabled when VictoriaLogs is unavailable")
	}
	if moduleMetrics := newModuleMetrics(nil, true); !moduleMetrics.enabled {
		t.Fatal("metrics must be enabled when VictoriaLogs sink is available")
	}
}

type failingTransport struct {
	calls atomic.Int32
}

func (t *failingTransport) Consume(_ []byte) error {
	t.calls.Add(1)
	return errors.New("transport failed")
}

func (t *failingTransport) Close(context.Context) error {
	return nil
}
