package preview

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCatchupKeepsOnlyRecentLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	body := "" +
		"{\"type\":\"meta\",\"name\":\"x\"}\n" +
		"{\"type\":\"gift\",\"ts\":1,\"user\":\"a\"}\n" +
		"{\"type\":\"danmaku\",\"ts\":10,\"text\":\"old\"}\n" +
		"{\"type\":\"gift\",\"ts\":100,\"user\":\"b\"}\n" +
		"{\"type\":\"danmaku\",\"ts\":120,\"text\":\"new\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := NewService().Catchup(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(bytes.Join(lines, []byte("\n")))
	if !bytes.Contains([]byte(got), []byte(`"meta"`)) {
		t.Fatalf("missing meta: %s", got)
	}
	if bytes.Contains([]byte(got), []byte(`"ts":1,`)) || bytes.Contains([]byte(got), []byte(`"ts":10,`)) {
		t.Fatalf("kept stale catchup: %s", got)
	}
	if !bytes.Contains([]byte(got), []byte(`"ts":100`)) || !bytes.Contains([]byte(got), []byte(`"ts":120`)) {
		t.Fatalf("dropped recent catchup: %s", got)
	}
}

func TestCatchupDropsIncompleteLastLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"danmaku\"}\n{\"type\":\"gif"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := NewService().Catchup(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines", len(lines))
	}
}

func TestRebasePreviewTsShiftsToJoinClock(t *testing.T) {
	got := RebasePreviewTs([]byte(`{"type":"gift","ts":120.5}`), 120)
	if !bytes.Contains(got, []byte(`"ts":0.5`)) {
		t.Fatalf("ts not rebased: %s", got)
	}
	meta := []byte(`{"type":"meta","name":"x"}`)
	if !bytes.Equal(RebasePreviewTs(meta, 120), meta) {
		t.Fatal("meta must stay untouched")
	}
}
