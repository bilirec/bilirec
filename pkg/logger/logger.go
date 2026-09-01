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

func (l Logger) Debug(msg string) { l.zap().Debug(msg) }
func (l Logger) Info(msg string)  { l.zap().Info(msg) }
func (l Logger) Warn(msg string)  { l.zap().Warn(msg) }
func (l Logger) Error(msg string) { l.zap().Error(msg) }

func (l Logger) Debugf(format string, args ...any) { l.logf(DebugLevel, format, args...) }
func (l Logger) Infof(format string, args ...any)  { l.logf(InfoLevel, format, args...) }
func (l Logger) Warnf(format string, args ...any)  { l.logf(WarnLevel, format, args...) }
func (l Logger) Errorf(format string, args ...any) { l.logf(ErrorLevel, format, args...) }

func (l Logger) Tracef(format string, args ...any) { l.logf(TraceLevel, format, args...) }

func (l Logger) logf(lvl zapcore.Level, format string, args ...any) {
	z := l.zap()
	if !z.Core().Enabled(lvl) {
		return
	}
	z.Log(lvl, fmt.Sprintf(format, args...))
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
		fields = append(fields, anyToField(key, kv[i+1]))
	}
	return fields
}

func anyToField(key string, v any) zap.Field {
	switch t := v.(type) {
	case string:
		return zap.String(key, t)
	case int:
		return zap.Int(key, t)
	case int64:
		return zap.Int64(key, t)
	case int32:
		return zap.Int32(key, t)
	case uint:
		return zap.Uint(key, t)
	case uint64:
		return zap.Uint64(key, t)
	case bool:
		return zap.Bool(key, t)
	case float64:
		return zap.Float64(key, t)
	case error:
		return zap.NamedError(key, t)
	case fmt.Stringer:
		return zap.Stringer(key, t)
	default:
		return zap.Any(key, t)
	}
}
