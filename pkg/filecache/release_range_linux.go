//go:build linux

package filecache

import (
	"os"

	"golang.org/x/sys/unix"
)

const syncFileRangeIntegrity = unix.SYNC_FILE_RANGE_WAIT_BEFORE |
	unix.SYNC_FILE_RANGE_WRITE |
	unix.SYNC_FILE_RANGE_WAIT_AFTER

// DropPageCacheRange advises the kernel to drop clean page cache for [offset, offset+length).
// offset/length are page-aligned internally; no-op when the aligned range is empty.
func DropPageCacheRange(f *os.File, offset, length int64) error {
	if f == nil {
		return nil
	}
	off, alignedLen, ok := alignedColdRange(offset, length)
	if !ok {
		return nil
	}
	return unix.Fadvise(int(f.Fd()), off, alignedLen, unix.FADV_DONTNEED)
}

// ReleaseColdRange syncs then drops page cache for [offset, offset+length).
// offset/length are page-aligned internally; no-op when the aligned range is empty.
func ReleaseColdRange(f *os.File, offset, length int64) error {
	if f == nil {
		return nil
	}
	off, alignedLen, ok := alignedColdRange(offset, length)
	if !ok {
		return nil
	}
	fd := int(f.Fd())
	if err := unix.SyncFileRange(fd, off, alignedLen, syncFileRangeIntegrity); err != nil {
		return err
	}
	return unix.Fadvise(fd, off, alignedLen, unix.FADV_DONTNEED)
}
