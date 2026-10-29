package recorder

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/danmaku"
	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/puzpuzpuz/xsync/v4"
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
	if !svc.IsRecording("一起看盛典-20260808_202002.vtt") {
		t.Fatal("expected vtt sidecar with matching stem to be recording")
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

func TestGetStatsSeparatesReadAndWritten(t *testing.T) {
	svc := &Service{
		recording: xsync.NewMap[int, *Info](),
		dm:        danmaku.New(&config.Config{}, nil, &metrics.Exporter{}, nil),
	}
	info := &Info{}
	info.startTime = time.Now()
	info.stats = &statsObserver{}
	info.stats.BytesRead.Store(5000)
	info.stats.BytesWritten.Store(3000)

	svc.recording.Store(42, info)

	stats, ok := svc.GetStats(42)
	if !ok {
		t.Fatal("expected stats")
	}
	if stats.BytesRead != 5000 {
		t.Fatalf("bytes_read = %d, want 5000", stats.BytesRead)
	}
	if stats.BytesWritten != 3000 {
		t.Fatalf("bytes_written = %d, want 3000", stats.BytesWritten)
	}
}

func TestGetStatsPipelineIoSums(t *testing.T) {
	svc := &Service{
		recording: xsync.NewMap[int, *Info](),
		dm:        danmaku.New(&config.Config{}, nil, &metrics.Exporter{}, nil),
	}
	info := &Info{}
	info.startTime = time.Now()
	info.stats = &statsObserver{}
	info.stats.FlushCount.Store(1)
	info.stats.FlushNs.Store(uint64(1500 * time.Millisecond))
	info.stats.SyncCount.Store(1)
	info.stats.SyncNs.Store(uint64(800 * time.Millisecond))
	info.stats.EnqueueWaits.Store(1)
	info.stats.EnqueueWaitNs.Store(uint64(25 * time.Millisecond))
	info.stats.TimelineGaps.Store(1)
	info.stats.TimelineGapNs.Store(uint64(500 * time.Millisecond))

	svc.recording.Store(7, info)

	stats, ok := svc.GetStats(7)
	if !ok {
		t.Fatal("expected stats")
	}
	if stats.FlushCount != 1 || stats.FlushMs != 1500 {
		t.Fatalf("flush: count=%d ms=%d", stats.FlushCount, stats.FlushMs)
	}
	if stats.SyncCount != 1 || stats.SyncMs != 800 {
		t.Fatalf("sync: count=%d ms=%d", stats.SyncCount, stats.SyncMs)
	}
	if stats.EnqueueWaits != 1 || stats.EnqueueWaitMs != 25 {
		t.Fatalf("enqueue: count=%d ms=%d", stats.EnqueueWaits, stats.EnqueueWaitMs)
	}
	if stats.TimelineGaps != 1 || stats.TimelineGapMs != 500 {
		t.Fatalf("timeline: count=%d ms=%d", stats.TimelineGaps, stats.TimelineGapMs)
	}
}
