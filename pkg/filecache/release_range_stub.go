//go:build !linux && !android

package filecache

import "os"

// ReleaseColdRange is a no-op on platforms without sync_file_range semantics.
func ReleaseColdRange(f *os.File, offset, length int64) error {
	return nil
}
