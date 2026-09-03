package logger

import (
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Options struct {
	Output io.Writer
	Level  zapcore.Level
	Debug  bool
	Color  *bool
}

var (
	initMu      sync.Mutex
	gen         atomic.Uint64
	root        atomic.Pointer[zap.Logger]
	level       zap.AtomicLevel
	sink        *swapWriter
	localCore   zapcore.Core
	remoteCore  zapcore.Core
	prettyColor bool
)

func Init(opts Options) {
	initMu.Lock()
	defer initMu.Unlock()
	initLocked(opts)
}

func SetDebug(on bool) {
	ensureInit()
	if on {
		level.SetLevel(DebugLevel)
		return
	}
	level.SetLevel(InfoLevel)
}

func SetLevel(lvl zapcore.Level) {
	ensureInit()
	level.SetLevel(lvl)
}

func Level() zapcore.Level {
	ensureInit()
	return level.Level()
}

func SetOutput(w io.Writer) {
	ensureInit()
	if w == nil {
		w = io.Discard
	}
	sink.Set(w)
}

func Sync() {
	if z := root.Load(); z != nil {
		_ = z.Sync()
	}
}

func ensureInit() {
	if root.Load() != nil {
		return
	}
	initMu.Lock()
	defer initMu.Unlock()
	if root.Load() != nil {
		return
	}
	initLocked(Options{})
}

func initLocked(opts Options) {
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}

	useColor := false
	if opts.Color != nil {
		useColor = *opts.Color
	} else {
		useColor = writerIsTTY(out)
	}
	if useColor {
		if f, ok := out.(*os.File); ok {
			out = colorable.NewColorable(f)
		}
	}
	prettyColor = useColor

	if sink == nil {
		sink = &swapWriter{w: out}
	} else {
		sink.Set(out)
	}

	lvl := InfoLevel
	if opts.Level != 0 {
		lvl = opts.Level
	}
	if opts.Debug {
		lvl = DebugLevel
	}
	if level == (zap.AtomicLevel{}) {
		level = zap.NewAtomicLevelAt(lvl)
	} else {
		level.SetLevel(lvl)
	}

	rebuildRoot()
}

func rebuildRoot() {
	prettyCore := zapcore.NewCore(newPrettyEncoder(prettyColor), sink, level)
	cores := []zapcore.Core{prettyCore}
	if localCore != nil {
		cores = append(cores, localCore)
	}
	if remoteCore != nil {
		cores = append(cores, remoteCore)
	}
	core := zapcore.NewTee(cores...)
	root.Store(zap.New(core))
	gen.Add(1)
}

func SetLocalCore(core zapcore.Core) {
	ensureInit()
	initMu.Lock()
	localCore = core
	rebuildRoot()
	initMu.Unlock()
}

func ClearLocalCore() {
	SetLocalCore(nil)
}

func SetRemoteCore(core zapcore.Core) {
	ensureInit()
	initMu.Lock()
	remoteCore = core
	rebuildRoot()
	initMu.Unlock()
}

func ClearRemoteCore() {
	SetRemoteCore(nil)
}

func getRoot() *zap.Logger {
	ensureInit()
	return root.Load()
}

func currentGen() uint64 {
	ensureInit()
	return gen.Load()
}

func build(name string, fields []zap.Field) *zap.Logger {
	z := getRoot()
	if name != "" {
		z = z.Named(name)
	}
	if len(fields) > 0 {
		z = z.With(fields...)
	}
	return z
}

func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

type swapWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *swapWriter) Set(w io.Writer) {
	s.mu.Lock()
	s.w = w
	s.mu.Unlock()
}

func (s *swapWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	w := s.w
	s.mu.Unlock()
	return w.Write(p)
}

func (s *swapWriter) Sync() error {
	s.mu.Lock()
	w := s.w
	s.mu.Unlock()
	if syncer, ok := w.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	return nil
}
