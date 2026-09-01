package logger

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	color := false
	Init(Options{Output: buf, Color: &color})
	t.Cleanup(func() {
		SetOutput(io.Discard)
		SetLevel(InfoLevel)
	})
	return buf
}

func TestPrettyFormatNamedAndFields(t *testing.T) {
	buf := capture(t)
	Named("recorder").With("room", 123).Info("錄製開始")
	Sync()

	got := buf.String()
	if !strings.Contains(got, "  INFO   recorder  錄製開始  room=123\n") &&
		!strings.Contains(got, "  INFO   recorder  錄製開始 room=123\n") {
		t.Fatalf("unexpected output: %q", got)
	}
	if strings.Contains(got, "{") {
		t.Fatalf("fields should not be JSON: %q", got)
	}
	if _, err := time.Parse("2006-01-02 15:04:05.000", strings.SplitN(strings.TrimSpace(got), "  ", 2)[0]); err != nil {
		t.Fatalf("timestamp: %v in %q", err, got)
	}
}

func TestMultilineMessagePreserved(t *testing.T) {
	buf := capture(t)
	L().Info("ffmpeg version\n  built with gcc")
	Sync()
	got := buf.String()
	if !strings.Contains(got, "ffmpeg version\n  built with gcc\n") {
		t.Fatalf("multiline not preserved: %q", got)
	}
}

func TestTraceHiddenAtInfo(t *testing.T) {
	buf := capture(t)
	Named("pipeline").Tracef("processor executed: %d", 1)
	Sync()
	if buf.Len() != 0 {
		t.Fatalf("trace should be silent at info: %q", buf.String())
	}
}

func TestWriterAt(t *testing.T) {
	buf := capture(t)
	w := Named("rest").WriterAt(InfoLevel)
	_, _ = w.Write([]byte("| 200 | 1ms | 127.0.0.1 | GET | /api | \n"))
	_ = w.Close()
	Sync()
	if !strings.Contains(buf.String(), "  INFO   rest  | 200 | 1ms | 127.0.0.1 | GET | /api |") {
		t.Fatalf("writer output: %q", buf.String())
	}
}

func TestPrettyQuotedAndNestedFields(t *testing.T) {
	buf := capture(t)
	Named("x").With("path", "a b", "ok", true, "meta", map[string]int{"n": 1}).Info("hit")
	Sync()
	got := buf.String()
	if !strings.Contains(got, `path="a b"`) {
		t.Fatalf("quoted string: %q", got)
	}
	if !strings.Contains(got, "ok=true") {
		t.Fatalf("bool: %q", got)
	}
	if !strings.Contains(got, `meta={"n":1}`) && !strings.Contains(got, `meta={"n": 1}`) {
		t.Fatalf("nested json: %q", got)
	}
}

func TestSetLevelAndNop(t *testing.T) {
	buf := capture(t)
	SetLevel(ErrorLevel)
	Named("x").Warn("hidden")
	Named("x").Error("shown")
	Nop().Error("discarded")
	Sync()
	got := buf.String()
	if strings.Contains(got, "hidden") || strings.Contains(got, "discarded") {
		t.Fatalf("unexpected: %q", got)
	}
	if !strings.Contains(got, "shown") {
		t.Fatalf("missing error: %q", got)
	}
}
