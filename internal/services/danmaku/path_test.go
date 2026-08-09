package danmaku

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathForVideo(t *testing.T) {
	got := PathForVideo(`/records/user-1/title-20260101.flv`, ".jsonl")
	want := `/records/user-1/title-20260101.jsonl`
	if filepath.FromSlash(got) != filepath.FromSlash(want) {
		t.Errorf("got %q want %q", got, want)
	}
	if PathForVideo("a.flv", "xml") != "a.xml" {
		t.Errorf("ext without dot: %q", PathForVideo("a.flv", "xml"))
	}
}

func TestResolveSidecarPrefersJSONL(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "seg.flv")
	jsonl := filepath.Join(dir, "seg.jsonl")
	xmlPath := filepath.Join(dir, "seg.xml")
	if err := os.WriteFile(jsonl, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xmlPath, []byte("<i></i>\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveSidecar(video)
	if err != nil {
		t.Fatal(err)
	}
	if got != jsonl {
		t.Errorf("got %q, want %q", got, jsonl)
	}
}

func TestResolveSidecarFallsBackToXML(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "seg.flv")
	xmlPath := filepath.Join(dir, "seg.xml")
	if err := os.WriteFile(xmlPath, []byte("<i></i>\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveSidecar(video)
	if err != nil {
		t.Fatal(err)
	}
	if got != xmlPath {
		t.Errorf("got %q, want %q", got, xmlPath)
	}
}

func TestResolveSidecarMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := ResolveSidecar(filepath.Join(dir, "missing.flv")); err == nil {
		t.Error("expected error")
	}
}
