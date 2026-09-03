package logger

import "go.uber.org/zap/zapcore"

// NewPrettyEncoder returns a pretty (human-readable) encoder.
// color controls whether ANSI color codes are included for terminal output.
func NewPrettyEncoder(color bool) zapcore.Encoder {
	return newPrettyEncoder(color)
}
