package logger

import (
	"os"
	"runtime/debug"
	"sync"
)
var (
	crashMu      sync.Mutex
	crashFile    *os.File
	crashPath    string
	crashEnabled bool
)

// EnableCrashCapture additionally redirects the runtime's fatal output
// (unrecovered panics, runtime fatal errors) to path, in addition to
// stderr. The file is opened with O_APPEND, which is safe to share with
// concurrent writers of the same file (lumberjack etc.) — both sides only
// ever append atomically and never seek or truncate. Note that the runtime
// keeps a dup of the descriptor it was given, so if the log file rotates
// afterwards (lumberjack MaxSize etc.), dumps keep landing in the original
// file until capture is re-registered; see NoteCrashLogWrite.
//
// Calling with the same path while enabled is a no-op. A different path
// switches the target (the new target only takes effect once
// debug.SetCrashOutput succeeds; on failure the previous one is kept).
func EnableCrashCapture(path string) error {
	crashMu.Lock()
	defer crashMu.Unlock()
	if crashEnabled && crashPath == path {
		return nil
	}
	return openCrashFileLocked(path)
}

// DisableCrashCapture unregisters the crash output registered by
// EnableCrashCapture. SetCrashOutput dups the descriptor internally, so
// closing the original *os.File here does not affect the runtime's copy.
// Safe to call multiple times.
func DisableCrashCapture() {
	crashMu.Lock()
	defer crashMu.Unlock()
	if !crashEnabled {
		return
	}
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	_ = crashFile.Close()
	crashFile = nil
	crashPath = ""
	crashEnabled = false
}

// NoteCrashLogWrite re-registers the crash output after the log file at path
// has been rotated. Call it after a batch write to that file. It is a no-op
// when capture is disabled, the target is a different path, or the file at
// path is still the same inode as the registered one.
func NoteCrashLogWrite(path string) {
	crashMu.Lock()
	defer crashMu.Unlock()
	if !crashEnabled || crashPath != path || crashFile == nil {
		return
	}
	cur, err := os.Stat(path)
	if err != nil {
		return
	}
	prev, err := crashFile.Stat()
	if err != nil || os.SameFile(prev, cur) {
		return
	}
	if err := openCrashFileLocked(path); err != nil {
		Named("logger").Warnf("重新注册崩溃输出失败 path=%s err=%v", path, err)
	}
}

// openCrashFileLocked opens path and registers it as the runtime crash output.
// Only on success does it replace crashFile/crashPath, so a failure leaves the
// previous target intact. Caller must hold crashMu.
func openCrashFileLocked(path string) error {
	f, err := openCrashAppend(path)
	if err != nil {
		return err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		return err
	}
	if crashFile != nil {
		_ = crashFile.Close()
	}
	crashFile = f
	crashPath = path
	crashEnabled = true
	return nil
}
