package logger

import (
	"bytes"
	"encoding/json"
	"strconv"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

const (
	timeLayout = "2006-01-02 15:04:05.000"

	ansiReset   = "\x1b[0m"
	ansiRed     = "\x1b[31m"
	ansiYellow  = "\x1b[33m"
	ansiGreen   = "\x1b[32m"
	ansiCyan    = "\x1b[36m"
	ansiMagenta = "\x1b[35m"
	ansiGray    = "\x1b[90m"
)

var bufPool = buffer.NewPool()

type prettyEncoder struct {
	zapcore.Encoder
	color bool
}

func newPrettyEncoder(color bool) zapcore.Encoder {
	cfg := zapcore.EncoderConfig{
		TimeKey:        zapcore.OmitKey,
		LevelKey:       zapcore.OmitKey,
		NameKey:        zapcore.OmitKey,
		CallerKey:      zapcore.OmitKey,
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     zapcore.OmitKey,
		StacktraceKey:  zapcore.OmitKey,
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	return &prettyEncoder{
		Encoder: zapcore.NewJSONEncoder(cfg),
		color:   color,
	}
}

func (e *prettyEncoder) Clone() zapcore.Encoder {
	return &prettyEncoder{
		Encoder: e.Encoder.Clone(),
		color:   e.color,
	}
}

func (e *prettyEncoder) EncodeEntry(ent zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	line := bufPool.Get()

	line.AppendString(ent.Time.Format(timeLayout))
	line.AppendString("  ")
	appendLevel(line, ent.Level, e.color)
	line.AppendString("  ")
	if ent.LoggerName != "" {
		line.AppendString(ent.LoggerName)
		line.AppendString("  ")
	}
	if ent.Message != "" {
		line.AppendString(ent.Message)
	}

	jsonBuf, err := e.Encoder.EncodeEntry(zapcore.Entry{}, fields)
	if err != nil {
		line.Free()
		return nil, err
	}
	appendLogfmtFields(line, jsonBuf.Bytes())
	jsonBuf.Free()

	if ent.Stack != "" {
		line.AppendByte('\n')
		line.AppendString(ent.Stack)
	}
	line.AppendByte('\n')
	return line, nil
}

func appendLevel(line *buffer.Buffer, lvl zapcore.Level, color bool) {
	name := levelName(lvl)
	if color {
		line.AppendString(levelColor(lvl))
		line.AppendString(name)
		line.AppendString(ansiReset)
		for i := len(name); i < 5; i++ {
			line.AppendByte(' ')
		}
		return
	}
	line.AppendString(name)
	for i := len(name); i < 5; i++ {
		line.AppendByte(' ')
	}
}

func levelName(lvl zapcore.Level) string {
	if lvl == TraceLevel {
		return "TRACE"
	}
	return lvl.CapitalString()
}

func levelColor(lvl zapcore.Level) string {
	switch {
	case lvl == TraceLevel:
		return ansiGray
	case lvl < zapcore.InfoLevel:
		return ansiCyan
	case lvl < zapcore.WarnLevel:
		return ansiGreen
	case lvl < zapcore.ErrorLevel:
		return ansiYellow
	case lvl < zapcore.DPanicLevel:
		return ansiRed
	default:
		return ansiMagenta
	}
}

func appendLogfmtFields(line *buffer.Buffer, raw []byte) {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return
	}
	if len(bytes.TrimSpace(raw[1:len(raw)-1])) == 0 {
		return
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return
		}
		key, ok := keyTok.(string)
		if !ok {
			return
		}
		var val any
		if err := dec.Decode(&val); err != nil {
			return
		}
		line.AppendByte(' ')
		line.AppendString(key)
		line.AppendByte('=')
		appendLogfmtValue(line, val)
	}
}

func appendLogfmtValue(line *buffer.Buffer, v any) {
	switch t := v.(type) {
	case nil:
		line.AppendString("null")
	case json.Number:
		line.AppendString(string(t))
	case bool:
		if t {
			line.AppendString("true")
		} else {
			line.AppendString("false")
		}
	case string:
		if needsQuote(t) {
			line.AppendString(strconv.Quote(t))
			return
		}
		line.AppendString(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			line.AppendString("?")
			return
		}
		line.AppendBytes(b)
	}
}

func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == '=' || c == '"' || c == '\\' {
			return true
		}
	}
	return false
}
