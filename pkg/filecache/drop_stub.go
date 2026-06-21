//go:build !linux && !android

package filecache

import "os"

// DropOpenFileCache is a no-op on platforms without posix_fadvise semantics.
func DropOpenFileCache(f *os.File) {}

// DropFilePageCache is a no-op on platforms without posix_fadvise semantics.
func DropFilePageCache(path string) {}
