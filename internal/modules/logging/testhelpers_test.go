package logging

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
	logsink "github.com/bilirec/bilirec/pkg/sink"
	"gopkg.in/natefinch/lumberjack.v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func newTestLumberjack(t *testing.T, path string) *logsink.FileTransport {
	t.Helper()
	return logsink.NewFileTransport(&lumberjack.Logger{
		Filename:   path,
		MaxSize:    20,
		MaxAge:     7,
		MaxBackups: 3,
	})
}

func newTestBufferedSink(t *testing.T, tr logsink.Transport, flushInterval time.Duration) *logsink.AsyncBufferedSink {
	t.Helper()
	s, err := logsink.NewAsyncBufferedSink(tr, logsink.Options{
		FlushInterval: flushInterval,
		Overflow:      logsink.OverflowBlock,
	})
	if err != nil {
		t.Fatalf("new async buffered sink: %v", err)
	}
	return s
}

func newTestVLogsTransport(t *testing.T, baseURL string, timeout time.Duration) *logsink.VLogsHTTPTransport {
	t.Helper()
	return logsink.NewVLogsHTTPTransport(logsink.VLogsHTTPTransportOptions{
		URL:     insertURLForTest(baseURL),
		Timeout: timeout,
	})
}

func insertURLForTest(base string) string {
	u, err := url.Parse(strings.TrimRight(base, "/") + "/insert/jsonline")
	if err != nil {
		panic(err)
	}
	return u.String()
}

func buildLocalCore(format string, sink *logsink.AsyncBufferedSink) zapcore.Core {
	var enc zapcore.Encoder
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jsonline":
		enc = logger.NewJsonLineEncoder()
	default:
		enc = logger.NewPrettyEncoder(false)
	}
	return zapcore.NewCore(enc, zapcore.AddSync(sink), levelEnabler{})
}

func buildRemoteCore(sink *logsink.AsyncBufferedSink, instance string) zapcore.Core {
	enc := logger.NewVLogsEncoder()
	return zapcore.NewCore(enc, zapcore.AddSync(sink), levelEnabler{}).With([]zapcore.Field{
		zap.String("app", "bilirec"),
		zap.String("instance", instance),
	})
}

type noCancelCtx struct{}

func (noCancelCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (noCancelCtx) Done() <-chan struct{}       { return nil }
func (noCancelCtx) Err() error                  { return nil }
func (noCancelCtx) Value(any) any               { return nil }
