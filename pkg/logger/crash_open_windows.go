//go:build windows

package logger

import (
	"os"

	"golang.org/x/sys/windows"
)

// openCrashAppend opens path for appending with FILE_SHARE_DELETE so the log
// rotator (lumberjack) can rename the file while the crash output still refers
// to it. The runtime dups this descriptor, and the dup inherits the share mode,
// so rotation keeps working after EnableCrashCapture.
func openCrashAppend(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		p,
		windows.FILE_APPEND_DATA|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
