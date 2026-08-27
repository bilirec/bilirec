package logger

import (
	"bytes"
	"sync"

	"go.uber.org/zap/zapcore"
)

type levelWriter struct {
	log   Logger
	level zapcore.Level
	mu    sync.Mutex
	buf   []byte
}

func newLevelWriter(log Logger, level zapcore.Level) *levelWriter {
	return &levelWriter{log: log, level: level}
}

func (w *levelWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(w.buf[:i], "\r"))
		w.buf = w.buf[i+1:]
		if line != "" {
			w.logLine(line)
		}
	}
	return len(p), nil
}

func (w *levelWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return nil
	}
	line := string(bytes.TrimRight(w.buf, "\r"))
	w.buf = nil
	if line != "" {
		w.logLine(line)
	}
	return nil
}

func (w *levelWriter) logLine(line string) {
	z := w.log.zap()
	if !z.Core().Enabled(w.level) {
		return
	}
	z.Log(w.level, line)
}
