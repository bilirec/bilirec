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

func TestParseRecordingSegmentFilenameOrdersByNameOnly(t *testing.T) {
	olderStamp, _, ok := ParseRecordingSegmentFilename("zzz-20200101_000000.flv")
	newerStamp, _, ok2 := ParseRecordingSegmentFilename("aaa-20260101_000000.flv")
	if !ok || !ok2 {
		t.Fatal("expected parseable recording filenames")
	}
	if olderStamp >= newerStamp {
		t.Fatal("expected older stamp to sort before newer by filename only")
	}

	_, seg0, ok0 := ParseRecordingSegmentFilename("t-20260101_000000.flv")
	_, seg2, ok2seg := ParseRecordingSegmentFilename("t-20260101_000000-2.flv")
	if !ok0 || !ok2seg {
		t.Fatal("expected segment filenames to parse")
	}
	if seg0 >= seg2 {
		t.Fatal("expected lower segment number on basename without suffix")
	}

	if _, _, bad := ParseRecordingSegmentFilename("readme.txt"); bad {
		t.Fatal("expected non-media file to be rejected")
	}
	if _, _, sidecar := ParseRecordingSegmentFilename("t-20260101_000000.jsonl"); sidecar {
		t.Fatal("expected danmaku sidecar without media ext to be rejected")
	}
}

func TestIsRecordingMediaFilename(t *testing.T) {
	media := []string{"a.mp4", "b.M4A", "c.ts", "seg.fmp4", "live.flv"}
	for _, name := range media {
		if !IsRecordingMediaFilename(name) {
			t.Fatalf("expected media name %q", name)
		}
	}
	nonMedia := []string{"danmaku.jsonl", "danmaku.xml", "notes.log", "readme", "dir"}
	for _, name := range nonMedia {
		if IsRecordingMediaFilename(name) {
			t.Fatalf("expected non-media name %q", name)
		}
	}
}
