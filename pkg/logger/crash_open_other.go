//go:build !windows

package logger

import "os"

// openCrashAppend opens path for appending. On non-Windows platforms open
// handles do not block rename/unlink, so the plain append open is enough.
func openCrashAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}
