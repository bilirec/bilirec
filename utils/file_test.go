package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStagingPath(t *testing.T) {
	t.Parallel()
	if got, want := StagingPath("/rec/a.mp4"), "/rec/a.mp4.tmp"; got != want {
		t.Fatalf("StagingPath = %q, want %q", got, want)
	}
}

func TestReplaceFile_OverwritesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmp := filepath.Join(dir, "out.mp4.tmp")
	final := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(tmp, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(tmp, final); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("final content = %q, want new", got)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("staging file should be gone after rename, stat err=%v", err)
	}
}

func TestRemoveIfExists_MissingIsOK(t *testing.T) {
	t.Parallel()
	if err := RemoveIfExists(filepath.Join(t.TempDir(), "nope.tmp")); err != nil {
		t.Fatal(err)
	}
}
