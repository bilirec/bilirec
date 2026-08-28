package logger

import (
	"fmt"
	"io"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	TraceLevel = zapcore.DebugLevel - 1
	DebugLevel = zapcore.DebugLevel
	InfoLevel  = zapcore.InfoLevel
	WarnLevel  = zapcore.WarnLevel
	ErrorLevel = zapcore.ErrorLevel
)

// Logger is a concrete zap wrapper. Named loggers rebind to the current root
// so Init / SetOutput stay visible to package-level vars created at init time.
type Logger struct {
	name   string
	fields []zap.Field
	bound  *zap.Logger
	gen    uint64
	nop    bool
}

func L() Logger {
	return Logger{}
}

func Named(name string) Logger {
	return Logger{name: name}
}

func Nop() Logger {
	return Logger{bound: zap.NewNop(), nop: true}
}

func Zap() *zap.Logger {
	return getRoot()
}

func (l Logger) Zap() *zap.Logger {
	return l.zap()
}

func (l Logger) Sugar() *zap.SugaredLogger {
	return l.zap().Sugar()
}

func (l Logger) With(kv ...any) Logger {
	if l.nop {
		return l
	}
	fields := append(append([]zap.Field{}, l.fields...), kvToFields(kv)...)
	g := currentGen()
	return Logger{
		name:   l.name,
		fields: fields,
		bound:  build(l.name, fields),
		gen:    g,
	}
}

func (l Logger) WithError(err error) Logger {
	if err == nil {
		return l
	}
	return l.With("error", err)
}

func (l Logger) Enabled(lvl zapcore.Level) bool {
	return l.zap().Core().Enabled(lvl)
}

func (l Logger) Debug(args ...any)                 { l.zap().Sugar().Debug(args...) }
func (l Logger) Info(args ...any)                  { l.zap().Sugar().Info(args...) }
func (l Logger) Warn(args ...any)                  { l.zap().Sugar().Warn(args...) }
func (l Logger) Error(args ...any)                 { l.zap().Sugar().Error(args...) }
func (l Logger) Debugf(format string, args ...any) { l.zap().Sugar().Debugf(format, args...) }
func (l Logger) Infof(format string, args ...any)  { l.zap().Sugar().Infof(format, args...) }
func (l Logger) Warnf(format string, args ...any)  { l.zap().Sugar().Warnf(format, args...) }
func (l Logger) Errorf(format string, args ...any) { l.zap().Sugar().Errorf(format, args...) }

func (l Logger) Tracef(format string, args ...any) {
	z := l.zap()
	if !z.Core().Enabled(TraceLevel) {
		return
	}
	z.Log(TraceLevel, fmt.Sprintf(format, args...))
}

func (l Logger) WriterAt(lvl zapcore.Level) io.WriteCloser {
	return newLevelWriter(l, lvl)
}

func (l Logger) zap() *zap.Logger {
	if l.nop {
		return l.bound
	}
	g := currentGen()
	if l.bound != nil && l.gen == g {
		return l.bound
	}
	return build(l.name, l.fields)
}

func kvToFields(kv []any) []zap.Field {
	if len(kv) == 0 {
		return nil
	}
	n := len(kv) / 2
	fields := make([]zap.Field, 0, n)
	for i := 0; i+1 < len(kv); i += 2 {
		key, _ := kv[i].(string)
		if key == "" {
			continue
		}
		fields = append(fields, zap.Any(key, kv[i+1]))
	}
	return fields
}
