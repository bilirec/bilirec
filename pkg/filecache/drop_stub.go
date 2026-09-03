//go:build !linux

package filecache

import "os"

// DropOpenFileCache is a no-op on platforms without posix_fadvise semantics.
func DropOpenFileCache(f *os.File) error { return nil }

// DropFilePageCache is a no-op on platforms without posix_fadvise semantics.
func DropFilePageCache(path string) error { return nil }
