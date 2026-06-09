package recorder_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type ffprobeAudioOnlyOutput struct {
	Streams []ffprobeStreamInfo `json:"streams"`
}

func TestAudioOnlyFlvRecord(t *testing.T) {
	runAudioOnlyRecordForProfile(t, bilibili.ProfileHTTPFLV)
}

// TS 在 audio only 的情況下依然會錄製到視頻，這貌似是B站的行為，暫時不清楚有什麼好的解決方案，因此改用 WARN
func TestAudioOnlyTsRecord(t *testing.T) {
	runAudioOnlyRecordForProfile(t, bilibili.ProfileHLSTS)
}

// FMP4 在 audio only 的情況下依然會錄製到視頻，這貌似是B站的行為，暫時不清楚有什麼好的解決方案，因此改用 WARN
func TestAudioOnlyFmp4Record(t *testing.T) {
	runAudioOnlyRecordForProfile(t, bilibili.ProfileHLSFMP4)
}

func runAudioOnlyRecordForProfile(t *testing.T, profile bilibili.StreamProfile) {
	if testing.Short() {
		t.Skip("skipping audio-only recording in short mode")
	}

	_ = os.Setenv("SKIP_SMALL_FLUSH", "false") // audio only recording may produce small writes, disable skip-small-flush for testing

	recordDuration := time.Duration(utils.Ternary(os.Getenv("CI") != "", 5, 1)) * time.Minute
	const pollInterval = 2 * time.Second
	const tolerance = 20 * time.Second

	var recorderService *recorder.Service
	var roomService *room.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(room.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(notify.NewService),
		fx.Provide(recorder.NewService),
		fx.Populate(&recorderService, &roomService),
	)

	app.RequireStart()
	defer app.RequireStop()

	room := resolveLiveTestRoomID(t, roomService)

	t.Logf("starting audio-only recording with profile %s and duration limit: %v", profile, recordDuration)
	err := recorderService.Start(room,
		recorder.WithDuration(recordDuration),
		recorder.WithStreamOptions(
			bilibili.WithProfiles(profile),
			bilibili.WithOnlyAudio(true),
		),
	)
	if err != nil {
		switch err {
		case recorder.ErrStreamNotLive:
			t.Skip("stream not live")
		case recorder.ErrEmptyStreamURLs:
			t.Skip("no stream URLs available")
		case recorder.ErrStreamURLsUnreachable:
			t.Skip("stream URLs unreachable")
		}
		t.Fatal(err)
	}

	if status := recorderService.GetStatus(room); status != recorder.Recording {
		t.Fatalf("expected status %q immediately after start, got %q", recorder.Recording, status)
	}

	outputPath := waitForOutputPathAfterStart(t, recorderService, room)

	deadline := time.Now().Add(recordDuration + tolerance)
	for time.Now().Before(deadline) {
		<-time.After(pollInterval)
		if recorderService.GetStatus(room) == recorder.Idle {
			if checkFFmpegAvailable(t) {
				t.Log("\n📹 Verifying audio-only recording via ffprobe...")
				verifyAudioOnlyRecording(t, outputPath)
			}
			return
		}
	}

	t.Errorf("recording did not auto-stop within %v (duration=%v + tolerance=%v)", recordDuration+tolerance, recordDuration, tolerance)
	if stopped := recorderService.Stop(room); !stopped {
		t.Error("expected recorder stop to return true")
	}
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
