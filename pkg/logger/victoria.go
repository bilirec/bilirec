package logger

import (
	"context"
	"net/url"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// AttachJSONLine adds a VictoriaLogs JSON Line core alongside the existing pretty console core.
// It rebuilds the root logger so init-time Named loggers rebind on the next use.
func AttachJSONLine(opts JSONLineOptions) *JSONLineSink {
	initMu.Lock()
	defer initMu.Unlock()

	if remoteSink != nil {
		_ = remoteSink.Stop(context.Background())
		remoteSink = nil
	}

	if strings.TrimSpace(opts.App) == "" {
		opts.App = "bilirec"
	}
	if strings.TrimSpace(opts.StreamFields) == "" {
		opts.StreamFields = "app,logger"
	}
	if strings.TrimSpace(opts.Instance) == "" {
		opts.Instance, _ = os.Hostname()
	}

	remoteSink = newJSONLineSink(opts)
	enc := newVictoriaEncoder()
	fields := []zapcore.Field{
		zap.String("app", opts.App),
	}
	if opts.Instance != "" {
		fields = append(fields, zap.String("instance", opts.Instance))
	}
	remoteCore = zapcore.NewCore(enc, remoteSink, level).With(fields)
	rebuildRoot()
	return remoteSink
}

func newVictoriaEncoder() zapcore.Encoder {
	cfg := zapcore.EncoderConfig{
		TimeKey:        "_time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      zapcore.OmitKey,
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "_msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    victoriaLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	return zapcore.NewJSONEncoder(cfg)
}

func victoriaLevelEncoder(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	if l == TraceLevel {
		enc.AppendString("trace")
		return
	}
	zapcore.LowercaseLevelEncoder(l, enc)
}

func buildInsertURL(base, streamFields string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	u, err := url.Parse(base + "/insert/jsonline")
	if err != nil {
		return base + "/insert/jsonline?_stream_fields=" + url.QueryEscape(streamFields) +
			"&_msg_field=_msg&_time_field=_time"
	}
	q := u.Query()
	q.Set("_stream_fields", streamFields)
	q.Set("_msg_field", "_msg")
	q.Set("_time_field", "_time")
	u.RawQuery = q.Encode()
	return u.String()
}
