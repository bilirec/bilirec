package danmaku

import (
	"os"
	"path/filepath"
	"testing"
)

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
