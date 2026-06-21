//go:build linux

package filecache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseColdRange_TempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cold.bin")
	data := make([]byte, 128*1024)
	for i := range data {
		data[i] = byte(i)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := ReleaseColdRange(f, 0, int64(len(data))); err != nil {
		t.Fatalf("ReleaseColdRange: %v", err)
	}
}

func TestDropPageCacheRange_TempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "drop.bin")
	data := make([]byte, 128*1024)
	for i := range data {
		data[i] = byte(i)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := DropPageCacheRange(f, 0, int64(len(data))); err != nil {
		t.Fatalf("DropPageCacheRange: %v", err)
	}
}
