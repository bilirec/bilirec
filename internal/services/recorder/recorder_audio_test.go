package recorder_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/utils"
)

type ffprobeAudioOnlyOutput struct {
	Streams []ffprobeStreamInfo `json:"streams"`
}

func TestAudioOnlyFlvRecord(t *testing.T) {
	runAudioOnlyRecordForProfile(t, bilibili.ProfileHTTPFLV)
}

func TestAudioOnlyTsRecord(t *testing.T) {
	runAudioOnlyRecordForProfile(t, bilibili.ProfileHLSTS)
}

func TestAudioOnlyFmp4Record(t *testing.T) {
	runAudioOnlyRecordForProfile(t, bilibili.ProfileHLSFMP4)
}

func runAudioOnlyRecordForProfile(t *testing.T, profile bilibili.StreamProfile) {
	if testing.Short() {
		t.Skip("skipping audio-only recording in short mode")
	}

	_ = os.Setenv("SKIP_SMALL_FLUSH", "false")

	recordWait := time.Duration(utils.Ternary(os.Getenv("CI") != "", 2, 1)) * time.Minute

	sess := newRecorderTestSession(t)
	room := resolveLiveTestRoomID(t, sess.Room)

	baseline := sess.Monitor.snapshotMemory(t, "baseline", true)

	t.Logf("starting audio-only recording with profile %s, will record for %v", profile, recordWait)
	startPhase, err := sess.Monitor.beginPhase("audio_only_start")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := sess.Recorder.Start(room,
		recorder.WithStreamOptions(
			bilibili.WithProfiles(profile),
			bilibili.WithOnlyAudio(true),
		),
	)
	startReport := startPhase.end(t)
	handleRecordingStartErr(t, startErr)
	logCPUPhase(t, startReport)

	if status := sess.Recorder.GetStatus(room); status != recorder.Recording {
		t.Fatalf("expected status %q immediately after start, got %q", recorder.Recording, status)
	}

	outputPath := waitForOutputPathAfterStart(t, sess.Recorder, room)

	_ = sess.Monitor.runRecordingProfiledWait(t, "audio_only_recording", recordWait)
	during := sess.Monitor.snapshotMemory(t, "during_recording", false)
	logMemoryDelta(t, baseline, during)

	t.Log("stopping recording manually")
	if stopped := sess.Recorder.Stop(room); !stopped {
		t.Error("expected recorder stop to return true")
	}

	time.Sleep(recorderTestSettleAfterStop)
	sess.Monitor.snapshotMemory(t, "after_stop", false)
	sess.Monitor.logAnalysisHints(t)

	if checkFFmpegAvailable(t) {
		t.Log("\n📹 Verifying audio-only recording via ffprobe...")
		verifyAudioOnlyRecording(t, outputPath)
	}
}

func verifyAudioOnlyRecording(t *testing.T, filePath string) {
	if filePath == "" {
		t.Fatal("output path is empty")
	}

	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("failed to stat recorded file %s: %v", filePath, err)
	}

	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_format",
		"-show_streams",
		"-of", "json",
		filePath,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("ffprobe failed: %v, stderr: %s", err, stderr.String())
	}

	var probe ffprobeAudioOnlyOutput
	if err := json.Unmarshal(stdout.Bytes(), &probe); err != nil {
		t.Fatalf("failed to parse ffprobe JSON: %v", err)
	}

	hasAudio := false
	hasVideo := false
	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "audio":
			hasAudio = true
			t.Logf("  - Audio stream: %d ch, %s Hz %s", stream.Channels, stream.SampleRate, stream.CodecName)
		case "video":
			hasVideo = true
			t.Logf("  - Video stream found: %dx%d %s", stream.Width, stream.Height, stream.CodecName)
		}
	}

	if !hasAudio {
		t.Fatal("expected audio stream in audio-only recording")
	}
	if hasVideo {
		t.Log("⚠️  Video stream found in audio-only recording")
	} else {
		t.Log("✓ ffprobe verified audio-only recording")
	}
}
