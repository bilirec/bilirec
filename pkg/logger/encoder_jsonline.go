package logger

import "go.uber.org/zap/zapcore"

// NewJsonLineEncoder returns a JSON-line encoder for local file logs.
// Field names follow the conventions of mainstream log-capture sidecars
// (Vector native defaults; Fluent Bit needs only `Time_Key timestamp`;
// Promtail/Filebeat map trivially): "timestamp", "message", "level".
func NewJsonLineEncoder() zapcore.Encoder {
	cfg := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  "stack",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    lowercaseLevelWithTrace,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	return zapcore.NewJSONEncoder(cfg)
}
