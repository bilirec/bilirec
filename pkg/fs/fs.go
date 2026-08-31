//go:build !cgo || !android

package fs

import (
	iofs "io/fs"
	"os"
)

func ReadDir(path string) ([]iofs.DirEntry, error) {
	return os.ReadDir(path)
}

func NotifyFileChanged(path string) {}
