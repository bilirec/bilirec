//go:build !linux

package filecache

import "os"

// ReleaseColdRange is a no-op on platforms without sync_file_range semantics.
func ReleaseColdRange(f *os.File, offset, length int64) error {
	return nil
}

// DropPageCacheRange is a no-op on platforms without posix_fadvise semantics.
func DropPageCacheRange(f *os.File, offset, length int64) error {
	return nil
}
