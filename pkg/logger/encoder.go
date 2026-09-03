package logger

import (
	"encoding/base64"
	"strconv"
	"time"

	"github.com/bytedance/sonic/encoder"
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
	color bool
	ns    string
	buf   *buffer.Buffer
}

func newPrettyEncoder(color bool) zapcore.Encoder {
	return &prettyEncoder{color: color, buf: bufPool.Get()}
}

func (e *prettyEncoder) Clone() zapcore.Encoder {
	c := &prettyEncoder{color: e.color, ns: e.ns, buf: bufPool.Get()}
	if e.buf != nil && e.buf.Len() > 0 {
		_, _ = c.buf.Write(e.buf.Bytes())
	}
	return c
}

func (e *prettyEncoder) writer() *logfmtWriter {
	if e.buf == nil {
		e.buf = bufPool.Get()
	}
	return &logfmtWriter{buf: e.buf, ns: e.ns}
}

func (e *prettyEncoder) OpenNamespace(key string) {
	e.ns = e.writer().pk(key) + "."
}

func (e *prettyEncoder) AddArray(key string, m zapcore.ArrayMarshaler) error {
	return e.writer().AddArray(key, m)
}

func (e *prettyEncoder) AddObject(key string, m zapcore.ObjectMarshaler) error {
	return e.writer().AddObject(key, m)
}

func (e *prettyEncoder) AddBinary(key string, v []byte)     { e.writer().AddBinary(key, v) }
func (e *prettyEncoder) AddByteString(key string, v []byte) { e.writer().AddByteString(key, v) }
func (e *prettyEncoder) AddBool(key string, v bool)         { e.writer().AddBool(key, v) }
func (e *prettyEncoder) AddComplex128(key string, v complex128) {
	e.writer().AddComplex128(key, v)
}
func (e *prettyEncoder) AddComplex64(key string, v complex64) {
	e.writer().AddComplex64(key, v)
}
func (e *prettyEncoder) AddDuration(key string, v time.Duration) {
	e.writer().AddDuration(key, v)
}
func (e *prettyEncoder) AddFloat64(key string, v float64) { e.writer().AddFloat64(key, v) }
func (e *prettyEncoder) AddFloat32(key string, v float32) { e.writer().AddFloat32(key, v) }
func (e *prettyEncoder) AddInt(key string, v int)         { e.writer().AddInt(key, v) }
func (e *prettyEncoder) AddInt64(key string, v int64)     { e.writer().AddInt64(key, v) }
func (e *prettyEncoder) AddInt32(key string, v int32)     { e.writer().AddInt32(key, v) }
func (e *prettyEncoder) AddInt16(key string, v int16)     { e.writer().AddInt16(key, v) }
func (e *prettyEncoder) AddInt8(key string, v int8)       { e.writer().AddInt8(key, v) }
func (e *prettyEncoder) AddString(key, v string)          { e.writer().AddString(key, v) }
func (e *prettyEncoder) AddTime(key string, v time.Time)  { e.writer().AddTime(key, v) }
func (e *prettyEncoder) AddUint(key string, v uint)       { e.writer().AddUint(key, v) }
func (e *prettyEncoder) AddUint64(key string, v uint64)   { e.writer().AddUint64(key, v) }
func (e *prettyEncoder) AddUint32(key string, v uint32)   { e.writer().AddUint32(key, v) }
func (e *prettyEncoder) AddUint16(key string, v uint16)   { e.writer().AddUint16(key, v) }
func (e *prettyEncoder) AddUint8(key string, v uint8)     { e.writer().AddUint8(key, v) }
func (e *prettyEncoder) AddUintptr(key string, v uintptr) { e.writer().AddUintptr(key, v) }
func (e *prettyEncoder) AddReflected(key string, v any) error {
	return e.writer().AddReflected(key, v)
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

	w := &logfmtWriter{buf: line, ns: e.ns}
	if e.buf != nil && e.buf.Len() > 0 {
		_, _ = line.Write(e.buf.Bytes())
	}
	for i := range fields {
		fields[i].AddTo(w)
	}

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

// lowercaseLevelWithTrace renders TraceLevel (custom level = DebugLevel - 1)
// as "trace"; other levels delegate to zapcore's lowercase encoder.
func lowercaseLevelWithTrace(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	if l == TraceLevel {
		enc.AppendString("trace")
		return
	}
	zapcore.LowercaseLevelEncoder(l, enc)
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

type logfmtWriter struct {
	buf        *buffer.Buffer
	ns         string
	inArray    bool
	arrNeedSep bool
}

func (w *logfmtWriter) pk(key string) string {
	if w.ns == "" {
		return key
	}
	return w.ns + key
}

func (w *logfmtWriter) beginKV(key string) {
	w.buf.AppendByte(' ')
	w.buf.AppendString(w.pk(key))
	w.buf.AppendByte('=')
}

func (w *logfmtWriter) arrSep() {
	if w.arrNeedSep {
		w.buf.AppendByte(',')
	}
	w.arrNeedSep = true
}

func (w *logfmtWriter) OpenNamespace(key string) {
	w.ns = w.pk(key) + "."
}

func (w *logfmtWriter) AddArray(key string, arr zapcore.ArrayMarshaler) error {
	w.beginKV(key)
	return w.AppendArray(arr)
}

func (w *logfmtWriter) AddObject(key string, obj zapcore.ObjectMarshaler) error {
	prev := w.ns
	w.ns = w.pk(key) + "."
	err := obj.MarshalLogObject(w)
	w.ns = prev
	return err
}

func (w *logfmtWriter) AddBinary(key string, v []byte) {
	w.AddString(key, base64.StdEncoding.EncodeToString(v))
}

func (w *logfmtWriter) AddByteString(key string, v []byte) {
	w.beginKV(key)
	appendQuoted(w.buf, string(v))
}

func (w *logfmtWriter) AddBool(key string, v bool) {
	w.beginKV(key)
	w.AppendBool(v)
}

func (w *logfmtWriter) AddComplex128(key string, v complex128) {
	w.beginKV(key)
	w.AppendComplex128(v)
}

func (w *logfmtWriter) AddComplex64(key string, v complex64) {
	w.AddComplex128(key, complex128(v))
}

func (w *logfmtWriter) AddDuration(key string, v time.Duration) {
	w.beginKV(key)
	w.AppendDuration(v)
}

func (w *logfmtWriter) AddFloat64(key string, v float64) {
	w.beginKV(key)
	w.AppendFloat64(v)
}

func (w *logfmtWriter) AddFloat32(key string, v float32) {
	w.AddFloat64(key, float64(v))
}

func (w *logfmtWriter) AddInt(key string, v int)       { w.AddInt64(key, int64(v)) }
func (w *logfmtWriter) AddInt64(key string, v int64)   { w.beginKV(key); w.AppendInt64(v) }
func (w *logfmtWriter) AddInt32(key string, v int32)   { w.AddInt64(key, int64(v)) }
func (w *logfmtWriter) AddInt16(key string, v int16)   { w.AddInt64(key, int64(v)) }
func (w *logfmtWriter) AddInt8(key string, v int8)     { w.AddInt64(key, int64(v)) }
func (w *logfmtWriter) AddUint(key string, v uint)     { w.AddUint64(key, uint64(v)) }
func (w *logfmtWriter) AddUint64(key string, v uint64) { w.beginKV(key); w.AppendUint64(v) }
func (w *logfmtWriter) AddUint32(key string, v uint32) { w.AddUint64(key, uint64(v)) }
func (w *logfmtWriter) AddUint16(key string, v uint16) { w.AddUint64(key, uint64(v)) }
func (w *logfmtWriter) AddUint8(key string, v uint8)   { w.AddUint64(key, uint64(v)) }
func (w *logfmtWriter) AddUintptr(key string, v uintptr) {
	w.AddUint64(key, uint64(v))
}

func (w *logfmtWriter) AddString(key, v string) {
	w.beginKV(key)
	appendQuoted(w.buf, v)
}

func (w *logfmtWriter) AddTime(key string, v time.Time) {
	w.beginKV(key)
	w.AppendTime(v)
}

func (w *logfmtWriter) AddReflected(key string, v any) error {
	w.beginKV(key)
	return w.AppendReflected(v)
}

func (w *logfmtWriter) AppendBool(v bool) {
	if w.inArray {
		w.arrSep()
	}
	if v {
		w.buf.AppendString("true")
		return
	}
	w.buf.AppendString("false")
}

func (w *logfmtWriter) AppendByteString(v []byte) {
	if w.inArray {
		w.arrSep()
	}
	appendQuoted(w.buf, string(v))
}

func (w *logfmtWriter) AppendComplex128(v complex128) {
	if w.inArray {
		w.arrSep()
	}
	w.buf.AppendByte('"')
	w.buf.AppendString(strconv.FormatFloat(real(v), 'g', -1, 64))
	w.buf.AppendByte('+')
	w.buf.AppendString(strconv.FormatFloat(imag(v), 'g', -1, 64))
	w.buf.AppendString("i\"")
}

func (w *logfmtWriter) AppendComplex64(v complex64) { w.AppendComplex128(complex128(v)) }

func (w *logfmtWriter) AppendFloat64(v float64) {
	if w.inArray {
		w.arrSep()
	}
	w.buf.AppendString(strconv.FormatFloat(v, 'g', -1, 64))
}

func (w *logfmtWriter) AppendFloat32(v float32) { w.AppendFloat64(float64(v)) }

func (w *logfmtWriter) AppendInt(v int) { w.AppendInt64(int64(v)) }

func (w *logfmtWriter) AppendInt64(v int64) {
	if w.inArray {
		w.arrSep()
	}
	w.buf.AppendString(strconv.FormatInt(v, 10))
}

func (w *logfmtWriter) AppendInt32(v int32) { w.AppendInt64(int64(v)) }
func (w *logfmtWriter) AppendInt16(v int16) { w.AppendInt64(int64(v)) }
func (w *logfmtWriter) AppendInt8(v int8)   { w.AppendInt64(int64(v)) }

func (w *logfmtWriter) AppendString(v string) {
	if w.inArray {
		w.arrSep()
	}
	appendQuoted(w.buf, v)
}

func (w *logfmtWriter) AppendUint(v uint) { w.AppendUint64(uint64(v)) }

func (w *logfmtWriter) AppendUint64(v uint64) {
	if w.inArray {
		w.arrSep()
	}
	w.buf.AppendString(strconv.FormatUint(v, 10))
}

func (w *logfmtWriter) AppendUint32(v uint32)   { w.AppendUint64(uint64(v)) }
func (w *logfmtWriter) AppendUint16(v uint16)   { w.AppendUint64(uint64(v)) }
func (w *logfmtWriter) AppendUint8(v uint8)     { w.AppendUint64(uint64(v)) }
func (w *logfmtWriter) AppendUintptr(v uintptr) { w.AppendUint64(uint64(v)) }

func (w *logfmtWriter) AppendDuration(v time.Duration) {
	if w.inArray {
		w.arrSep()
	}
	appendQuoted(w.buf, v.String())
}

func (w *logfmtWriter) AppendTime(v time.Time) {
	if w.inArray {
		w.arrSep()
	}
	appendQuoted(w.buf, v.UTC().Format("2006-01-02T15:04:05.000Z0700"))
}

func (w *logfmtWriter) AppendArray(arr zapcore.ArrayMarshaler) error {
	if w.inArray {
		w.arrSep()
	}
	w.buf.AppendByte('[')
	nested := &logfmtWriter{buf: w.buf, inArray: true}
	err := arr.MarshalLogArray(nested)
	w.buf.AppendByte(']')
	return err
}

func (w *logfmtWriter) AppendObject(obj zapcore.ObjectMarshaler) error {
	if w.inArray {
		w.arrSep()
		w.buf.AppendByte('{')
		nested := &logfmtWriter{buf: w.buf}
		err := obj.MarshalLogObject(nested)
		w.buf.AppendByte('}')
		return err
	}
	return obj.MarshalLogObject(w)
}

func (w *logfmtWriter) AppendReflected(v any) error {
	if w.inArray {
		w.arrSep()
	}
	var raw []byte
	if err := encoder.EncodeInto(&raw, v, 0); err != nil {
		w.buf.AppendString("?")
		return err
	}
	w.buf.AppendBytes(raw)
	return nil
}

func appendQuoted(buf *buffer.Buffer, s string) {
	if needsQuote(s) {
		buf.AppendString(strconv.Quote(s))
		return
	}
	buf.AppendString(s)
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
