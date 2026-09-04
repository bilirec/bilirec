package logger

import "go.uber.org/zap/zapcore"

// NewVLogsEncoder returns the encoder used for VLogs remote transport (JSON-line).
// The field names (_time, _msg) are part of the VLogs insert contract and must
// stay in sync with insertURL's _msg_field/_time_field query parameters.
func NewVLogsEncoder() zapcore.Encoder {
	cfg := zapcore.EncoderConfig{
		TimeKey:        "_time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      zapcore.OmitKey,
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "_msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    lowercaseLevelWithTrace,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	return zapcore.NewJSONEncoder(cfg)
}
