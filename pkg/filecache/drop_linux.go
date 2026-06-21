//go:build linux || android

package filecache

import (
	"os"

	"golang.org/x/sys/unix"
)

// DropOpenFileCache advises the kernel to release page cache for an open file.
func DropOpenFileCache(f *os.File) {
	if f == nil {
		return
	}
	_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}

// DropFilePageCache opens path read-only and drops its page cache.
func DropFilePageCache(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	DropOpenFileCache(f)
}
