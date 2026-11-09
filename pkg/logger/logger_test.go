package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"
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

func TestJSONEncodersPreserveNanosecondTime(t *testing.T) {
	want := time.Date(2026, 9, 4, 7, 35, 4, 463821917, time.FixedZone("CST", 8*3600))
	ent := zapcore.Entry{Level: zapcore.InfoLevel, Time: want, Message: "hit"}

	cases := []struct {
		name string
		enc  zapcore.Encoder
		key  string
	}{
		{name: "vlogs", enc: NewVLogsEncoder(), key: "_time"},
		{name: "jsonline", enc: NewJsonLineEncoder(), key: "timestamp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := tc.enc.EncodeEntry(ent, nil)
			if err != nil {
				t.Fatalf("EncodeEntry: %v", err)
			}
			var row map[string]any
			if err := json.Unmarshal(buf.Bytes(), &row); err != nil {
				t.Fatalf("json: %v in %q", err, buf.String())
			}
			raw, _ := row[tc.key].(string)
			got, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				t.Fatalf("parse %s=%q: %v", tc.key, raw, err)
			}
			if !got.Equal(want) {
				t.Fatalf("%s: got %v want %v (raw=%q)", tc.key, got, want, raw)
			}
		})
	}
}

func TestRecoverPanicLogsStackAndCallsCallback(t *testing.T) {
	buf := capture(t)
	called := make(chan any, 1)
	func() {
		defer L().RecoverPanic("测试协程", func(p any) {
			called <- p
		})
		panic("boom")
	}()
	Sync()
	got := buf.String()
	if !strings.Contains(got, "测试协程 panic：boom") {
		t.Fatalf("panic message missing: %q", got)
	}
	if !strings.Contains(got, "TestRecoverPanicLogsStackAndCallsCallback") {
		t.Fatalf("stack trace missing test frame: %q", got)
	}
	select {
	case p := <-called:
		if p != "boom" {
			t.Fatalf("callback got %v, want boom", p)
		}
	default:
		t.Fatal("callback not invoked")
	}
}

func TestRecoverPanicMultipleCallbacksRunInOrder(t *testing.T) {
	capture(t)
	var order []string
	func() {
		defer L().RecoverPanic("多回调", func(any) { order = append(order, "first") },
			func(any) { order = append(order, "second") })
		panic("x")
	}()
	Sync()
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("callback order: %v", order)
	}
}

func TestRecoverPanicNoPanicNoCallback(t *testing.T) {
	called := false
	func() {
		defer L().RecoverPanic("无 panic", func(any) { called = true })
	}()
	if called {
		t.Fatal("callback should not fire without panic")
	}
}

func TestRecoverPanicLogErrorBeforeCallbackPanics(t *testing.T) {
	buf := capture(t)
	func() {
		// RecoverPanic runs first (registered later = LIFO), logs the origin
		// panic, then the callback's own panic propagates to this guard.
		defer func() {
			if p := recover(); p != "callback boom" {
				t.Fatalf("callback panic should propagate, got %v", p)
			}
		}()
		defer L().RecoverPanic("回调炸", func(any) { panic("callback boom") })
		panic("origin")
	}()
	Sync()
	got := buf.String()
	if !strings.Contains(got, "回调炸 panic：origin") {
		t.Fatalf("origin panic evidence lost when callback panics: %q", got)
	}
}

func TestEnableCrashCaptureSamePathNoopDifferentPathSwitches(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.log")
	second := filepath.Join(dir, "second.log")
	if err := EnableCrashCapture(first); err != nil {
		t.Fatalf("first enable: %v", err)
	}
	// Same path while enabled is a no-op: the registered file stays the same inode.
	before := crashFile
	if err := EnableCrashCapture(first); err != nil {
		t.Fatalf("same-path enable: %v", err)
	}
	if crashFile != before {
		t.Fatal("same-path enable should not swap the crash file")
	}
	// A different path switches the target.
	if err := EnableCrashCapture(second); err != nil {
		t.Fatalf("different-path enable: %v", err)
	}
	if crashPath != second {
		t.Fatalf("crashPath = %q, want %q", crashPath, second)
	}
	onDisk, err := os.Stat(second)
	if err != nil {
		t.Fatalf("second crash file should exist: %v", err)
	}
	prev, err := crashFile.Stat()
	if err != nil || !os.SameFile(prev, onDisk) {
		t.Fatalf("crashFile should point at second.log")
	}

	DisableCrashCapture()
	DisableCrashCapture() // double disable is safe
	if err := EnableCrashCapture(first); err != nil {
		t.Fatalf("re-enable after disable: %v", err)
	}
	DisableCrashCapture()
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("crash file should exist: %v", err)
	}
}

func TestNoteCrashLogWriteFollowsRotatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := EnableCrashCapture(path); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer DisableCrashCapture()

	// Rotate: move the registered file away and let lumberjack create a new one.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	_ = f.Close()

	// A different path must not switch the target.
	NoteCrashLogWrite(filepath.Join(dir, "elsewhere.log"))
	if crashPath != path {
		t.Fatalf("crashPath changed to %q", crashPath)
	}

	NoteCrashLogWrite(path)
	onDisk, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat new file: %v", err)
	}
	prev, err := crashFile.Stat()
	if err != nil || !os.SameFile(prev, onDisk) {
		t.Fatalf("NoteCrashLogWrite should re-register the rotated file")
	}
}

func TestEnableCrashCaptureBadPathFails(t *testing.T) {
	err := EnableCrashCapture(filepath.Join(t.TempDir(), "no-such-dir", "x.log"))
	if err == nil {
		DisableCrashCapture()
		t.Fatal("expected error for bad path")
	}
	if !strings.Contains(err.Error(), "no-such-dir") && !os.IsNotExist(err) &&
		!strings.Contains(fmt.Sprintf("%v", err), "system cannot find") {
		t.Fatalf("unexpected error: %v", err)
	}
}
