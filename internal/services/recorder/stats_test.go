package recorder

import (
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/pkg/ds"
)

func TestIsRecording_DanmakuSidecarStem(t *testing.T) {
	svc := &Service{writingFiles: ds.NewSyncedSet[string]()}
	svc.writingFiles.Add("一起看盛典-20260808_202002.flv")

	if !svc.IsRecording("一起看盛典-20260808_202002.flv") {
		t.Fatal("expected active video file to be recording")
	}
	jsonlPath := filepath.Join("泽音Melody-1947277414", "一起看盛典-20260808_202002.jsonl")
	if !svc.IsRecording(jsonlPath) {
		t.Fatal("expected jsonl sidecar with matching stem to be recording")
	}
	if !svc.IsRecording("一起看盛典-20260808_202002.xml") {
		t.Fatal("expected xml sidecar with matching stem to be recording")
	}
	if svc.IsRecording("一起看盛典-20260808_202002.mp4") {
		t.Fatal("mp4 with same stem should not inherit recording status from flv segment")
	}
	if svc.IsRecording("other-segment.jsonl") {
		t.Fatal("unrelated sidecar should not be recording")
	}
}

func TestRecordingFileStem(t *testing.T) {
	if got := recordingFileStem("seg.flv"); got != "seg" {
		t.Fatalf("got %q", got)
	}
	if got := recordingFileStem("noext"); got != "" {
		t.Fatalf("expected empty stem for extensionless name, got %q", got)
	}
}
