package fs

import (
	iofs "io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadDirMatchesOs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("ReadDir len=%d, os.ReadDir len=%d", len(got), len(want))
	}
	names := map[string]bool{}
	for _, e := range got {
		names[e.Name()] = e.IsDir()
	}
	for _, e := range want {
		isDir, ok := names[e.Name()]
		if !ok {
			t.Fatalf("missing %s", e.Name())
		}
		if isDir != e.IsDir() {
			t.Fatalf("%s IsDir got %v want %v", e.Name(), isDir, e.IsDir())
		}
	}
}

func TestNotifyFileChangedNoop(t *testing.T) {
	NotifyFileChanged("")
	NotifyFileChanged(t.TempDir())
}

func TestParseListJSON(t *testing.T) {
	entries, err := parseListJSON(`[{"name":"a.mp4","isDir":false,"size":12},{"name":"room","isDir":true,"size":0},{"name":"."}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len=%d", len(entries))
	}
	if entries[0].Name() != "a.mp4" || entries[0].IsDir() {
		t.Fatalf("file entry %+v", entries[0])
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 12 {
		t.Fatalf("size=%d", info.Size())
	}
	if !entries[1].IsDir() || entries[1].Name() != "room" {
		t.Fatalf("dir entry %+v", entries[1])
	}
}

func TestParseListJSONInvalid(t *testing.T) {
	if _, err := parseListJSON("{"); err == nil {
		t.Fatal("expected error")
	}
}

func TestMergeDirEntries(t *testing.T) {
	native := []iofs.DirEntry{
		dirEnt{name: "room-1", isDir: true},
		dirEnt{name: "visible.flv", isDir: false, size: 3},
	}
	media := []iofs.DirEntry{
		dirEnt{name: "hidden.mp4", isDir: false, size: 9},
		dirEnt{name: "room-1", isDir: false, size: 1},
	}
	merged := mergeDirEntries(native, media)
	got := map[string]bool{}
	sizes := map[string]int64{}
	for _, e := range merged {
		got[e.Name()] = e.IsDir()
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		sizes[e.Name()] = info.Size()
	}
	if !got["room-1"] {
		t.Fatal("expected native directory to win")
	}
	if got["hidden.mp4"] || got["visible.flv"] {
		t.Fatal("mp4/flv should be files")
	}
	if sizes["hidden.mp4"] != 9 {
		t.Fatalf("hidden size=%d", sizes["hidden.mp4"])
	}
	if sizes["visible.flv"] != 3 {
		t.Fatalf("visible size=%d", sizes["visible.flv"])
	}
}
