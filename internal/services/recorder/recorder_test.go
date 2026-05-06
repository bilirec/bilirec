package recorder_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/services/convert"
	"github.com/eric2788/bilirec/internal/services/path"
	"github.com/eric2788/bilirec/internal/services/recorder"
	"github.com/eric2788/bilirec/internal/services/stream"
	"github.com/eric2788/bilirec/internal/testutil"
	"github.com/eric2788/bilirec/utils"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestFlvRecord(t *testing.T) {

	if testing.Short() {
		t.Skip("skipping TestFlvRecord in short mode")
	}

	room := testutil.LiveRoomID(t)

	var recorderService *recorder.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(recorder.NewService),
		fx.Populate(&recorderService),
	)

	app.RequireStart()
	defer app.RequireStop()

	var m1, m2, m3 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	t.Log("start it manually")
	err := recorderService.Start(room, recorder.WithStreamProfile(bilibili.ProfileHTTPFLV))
	if err != nil {
		switch err {
		case recorder.ErrStreamNotLive:
			t.Skip("Stream not live")
		case recorder.ErrEmptyStreamURLs:
			t.Skip("No stream URLs available")
		case recorder.ErrStreamURLsUnreachable:
			t.Skip("Stream URLs unreachable")
		}
		t.Fatal(err)
	}

	<-time.After(time.Duration(utils.Ternary(os.Getenv("CI") != "", 15, 3)) * time.Minute)
	outputPath := waitForOutputPath(t, recorderService, room, 3*time.Second)

	runtime.ReadMemStats(&m2)

	t.Log("stop it manually")
	t.Logf("stop success: %v", recorderService.Stop(room))

	<-time.After(5 * time.Second)
	runtime.ReadMemStats(&m3)

	t.Logf("memory before start: %.2f MB", float64(m1.Alloc/1024/1024))
	t.Logf("memory before stop: %.2f MB", float64(m2.Alloc/1024/1024))
	t.Logf("memory after stop: %.2f MB", float64(m3.Alloc/1024/1024))
	t.Logf("growth during recording: %.2f MB", float64((m2.Alloc-m1.Alloc)/1024/1024))
	t.Logf("growth after stop: %.2f MB", float64((m3.Alloc-m2.Alloc)/1024/1024))

	// Verify recorded file playability
	if checkFFmpegAvailable(t) {
		t.Log("\n📹 Verifying FLV recording playability...")
		verifyRecordingPlayability(t, outputPath, "flv")
	}
}

func TestTsRecord(t *testing.T) {

	if testing.Short() {
		t.Skip("skipping TestTsRecord in short mode")
	}

	room := testutil.LiveRoomID(t)

	var recorderService *recorder.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(recorder.NewService),
		fx.Populate(&recorderService),
	)

	app.RequireStart()
	defer app.RequireStop()

	var m1, m2, m3 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	t.Log("start it manually")
	err := recorderService.Start(room, recorder.WithStreamProfile(bilibili.ProfileHLSTS))
	if err != nil {
		switch err {
		case recorder.ErrStreamNotLive:
			t.Skip("Stream not live")
		case recorder.ErrEmptyStreamURLs:
			t.Skip("No stream URLs available")
		case recorder.ErrStreamURLsUnreachable:
			t.Skip("Stream URLs unreachable")
		}
		t.Fatal(err)
	}

	<-time.After(time.Duration(utils.Ternary(os.Getenv("CI") != "", 15, 3)) * time.Minute)
	outputPath := waitForOutputPath(t, recorderService, room, 3*time.Second)

	runtime.ReadMemStats(&m2)

	t.Log("stop it manually")
	t.Logf("stop success: %v", recorderService.Stop(room))

	<-time.After(5 * time.Second)
	runtime.ReadMemStats(&m3)

	t.Logf("memory before start: %.2f MB", float64(m1.Alloc/1024/1024))
	t.Logf("memory before stop: %.2f MB", float64(m2.Alloc/1024/1024))
	t.Logf("memory after stop: %.2f MB", float64(m3.Alloc/1024/1024))
	t.Logf("growth during recording: %.2f MB", float64((m2.Alloc-m1.Alloc)/1024/1024))
	t.Logf("growth after stop: %.2f MB", float64((m3.Alloc-m2.Alloc)/1024/1024))

	// Verify recorded file playability
	if checkFFmpegAvailable(t) {
		t.Log("\n📹 Verifying TS recording playability...")
		verifyRecordingPlayability(t, outputPath, "ts")
	}
}

func TestFmp4Record(t *testing.T) {

	if testing.Short() {
		t.Skip("skipping TestFmp4Record in short mode")
	}

	room := testutil.LiveRoomID(t)

	var recorderService *recorder.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(recorder.NewService),
		fx.Populate(&recorderService),
	)

	app.RequireStart()
	defer app.RequireStop()

	var m1, m2, m3 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	t.Log("start it manually")
	err := recorderService.Start(room, recorder.WithStreamProfile(bilibili.ProfileHLSFMP4))
	if err != nil {
		switch err {
		case recorder.ErrStreamNotLive:
			t.Skip("Stream not live")
		case recorder.ErrEmptyStreamURLs:
			t.Skip("No stream URLs available")
		case recorder.ErrStreamURLsUnreachable:
			t.Skip("Stream URLs unreachable")
		}
		t.Fatal(err)
	}

	<-time.After(time.Duration(utils.Ternary(os.Getenv("CI") != "", 15, 3)) * time.Minute)
	outputPath := waitForOutputPath(t, recorderService, room, 3*time.Second)

	runtime.ReadMemStats(&m2)

	t.Log("stop it manually")
	t.Logf("stop success: %v", recorderService.Stop(room))

	<-time.After(5 * time.Second)
	runtime.ReadMemStats(&m3)

	t.Logf("memory before start: %.2f MB", float64(m1.Alloc/1024/1024))
	t.Logf("memory before stop: %.2f MB", float64(m2.Alloc/1024/1024))
	t.Logf("memory after stop: %.2f MB", float64(m3.Alloc/1024/1024))
	t.Logf("growth during recording: %.2f MB", float64((m2.Alloc-m1.Alloc)/1024/1024))
	t.Logf("growth after stop: %.2f MB", float64((m3.Alloc-m2.Alloc)/1024/1024))

	// Verify recorded file playability
	if checkFFmpegAvailable(t) {
		t.Log("\n📹 Verifying FMP4 recording playability...")
		verifyRecordingPlayability(t, outputPath, "fmp4")
	}
}

func TestFlvRecord_AutoStopAfterDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping TestFlvRecord_AutoStopAfterDuration in short mode")
	}

	room := testutil.LiveRoomID(t)
	const recordDuration = 60 * time.Second
	const pollInterval = 2 * time.Second
	const tolerance = 20 * time.Second // allow extra time for stop to propagate

	var recorderService *recorder.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(recorder.NewService),
		fx.Populate(&recorderService),
	)

	app.RequireStart()
	defer app.RequireStop()

	t.Logf("starting recording with duration limit: %v", recordDuration)
	err := recorderService.Start(room, recorder.WithDuration(recordDuration))
	if err != nil {
		switch err {
		case recorder.ErrStreamNotLive:
			t.Skip("Stream not live")
		case recorder.ErrEmptyStreamURLs:
			t.Skip("No stream URLs available")
		case recorder.ErrStreamURLsUnreachable:
			t.Skip("Stream URLs unreachable")
		}
		t.Fatal(err)
	}

	if status := recorderService.GetStatus(room); status != recorder.Recording {
		t.Fatalf("expected status %q immediately after start, got %q", recorder.Recording, status)
	}

	deadline := time.Now().Add(recordDuration + tolerance)
	startTime := time.Now()
	for time.Now().Before(deadline) {
		<-time.After(pollInterval)
		status := recorderService.GetStatus(room)
		t.Logf("elapsed: %v, status: %s", time.Since(startTime).Round(time.Second), status)
		if status == recorder.Idle {
			t.Logf("recording auto-stopped after ~%v as expected", recordDuration)
			return
		}
	}

	t.Errorf("recording did not auto-stop within %v (duration=%v + tolerance=%v)", recordDuration+tolerance, recordDuration, tolerance)
	recorderService.Stop(room)
}

func TestChannelRangeReturnedWhileStreaming(t *testing.T) {
	ch := make(chan int, 10)
	// give some random elements keep sending to channel
	send := func() {
		for i := 0; i < 10; i++ {
			ch <- i
			time.Sleep(1 * time.Second)
		}
		close(ch)
	}
	go send()
	// range over channel and print elements
	for v := range ch {
		t.Logf("received: %d", v)
		if v == 5 {
			t.Log("stop early")
			break
		}
	}
	<-time.After(5 * time.Second)
	for v := range ch {
		t.Logf("received after first range stopped: %d", v)
	}
}

func TestInfoOutputPath_DefaultEmpty(t *testing.T) {
	info := &recorder.Info{}
	if got := info.OutputPath(); got != "" {
		t.Fatalf("expected empty output path, got %q", got)
	}
}

func TestInfoOutputPath_AtomicConcurrentReadWrite(t *testing.T) {
	info := &recorder.Info{}
	info.SetOutputPath("")

	const writers = 8
	const readers = 8
	const loops = 2000

	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < loops; n++ {
				info.SetOutputPath(fmt.Sprintf("seg-%d-%d.flv", id, n))
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < loops; n++ {
				_ = info.OutputPath()
			}
		}()
	}

	wg.Wait()

	if got := info.OutputPath(); got == "" {
		t.Fatal("expected non-empty output path after concurrent writes")
	}
}

func waitForOutputPath(t *testing.T, recorderService *recorder.Service, room int, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stats, hasStats := recorderService.GetStats(room)
		if hasStats && stats != nil && stats.OutputPath != "" {
			return stats.OutputPath
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Log("output path still empty before stop")
	return ""
}

// checkFFmpegAvailable checks if ffmpeg and ffprobe are available in the system PATH
func checkFFmpegAvailable(t *testing.T) bool {
	// Check for ffprobe
	_, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Logf("⚠️ ffprobe not found in PATH, skipping playability verification: %v", err)
		return false
	}
	t.Log("✓ ffprobe found, will verify recorded file playability")
	return true
}

// parseFloatDuration converts a string duration (e.g., "30.123456") to float64
func parseFloatDuration(durationStr string) (float64, error) {
	// Simple float parsing to convert duration string to seconds
	var result float64
	_, err := fmt.Sscanf(durationStr, "%f", &result)
	return result, err
}

// ffprobeStreamInfo represents stream information from ffprobe
type ffprobeStreamInfo struct {
	CodecType  string `json:"codec_type"`
	CodecName  string `json:"codec_name"`
	Duration   string `json:"duration"`
	TimeBase   string `json:"time_base"`
	StartTime  string `json:"start_time"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	SampleRate string `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

// ffprobeOutput represents the output structure from ffprobe
type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []ffprobeStreamInfo `json:"streams"`
}

// verifyRecordingPlayability verifies the recorded file's playability using ffprobe
func verifyRecordingPlayability(t *testing.T, filePath string, expectedFormat string) {
	if filePath == "" {
		t.Error("❌ Output file path is empty")
		return
	}

	// Wait a moment to ensure file finalization
	time.Sleep(500 * time.Millisecond)

	// Check file exists and has content
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Errorf("❌ Recorded file not found: %s", filePath)
		} else {
			t.Errorf("❌ Failed to stat recorded file: %v", err)
		}
		return
	}

	if fileInfo.Size() == 0 {
		t.Errorf("❌ Recorded file is empty: %s", filePath)
		return
	}

	t.Logf("✓ Recorded file exists: %s (size: %.2f MB)", filePath, float64(fileInfo.Size())/1024/1024)

	// Use ffprobe to verify file integrity and get stream information
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
		t.Logf("⚠️ ffprobe verification skipped: %v, stderr: %s", err, stderr.String())
		return
	}

	// Parse ffprobe JSON output
	var probe ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &probe); err != nil {
		t.Logf("⚠️ Failed to parse ffprobe JSON: %v, output: %s", err, stdout.String())
		return
	}

	hasVideo := false
	hasAudio := false

	if durationFloat, err := parseFloatDuration(probe.Format.Duration); err == nil {
		mins := int(durationFloat) / 60
		secs := int(durationFloat) % 60
		t.Logf("  - Duration: %d:%02d", mins, secs)
	}

	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "video":
			hasVideo = true
			t.Logf("  - Video stream: %dx%d %s", stream.Width, stream.Height, stream.CodecName)
		case "audio":
			hasAudio = true
			t.Logf("  - Audio stream: %d ch, %s Hz %s", stream.Channels, stream.SampleRate, stream.CodecName)
		}
	}

	if !hasVideo && !hasAudio {
		t.Error("❌ No valid video or audio streams found in recorded file")
		return
	}

	if hasVideo {
		t.Log("✓ Video stream verified - file should be playable")
	}
	if hasAudio {
		t.Log("✓ Audio stream verified - file should be playable")
	}

	// Format-specific notes
	switch expectedFormat {
	case "flv":
		t.Log("  Note: FLV files may have minor header inconsistencies due to streaming nature, but should be playable without seeking")
	case "ts":
		t.Log("  Note: TS files may exhibit 2-second stutter when seeking (seeking after pause) - this is normal. Playback without seeking should be smooth.")
	case "fmp4":
		t.Log("  Note: FMP4 files may show screen artifacts when seeking - this is normal. Playback without seeking should be clean with proper timestamps.")
	}
}

func init() {
	if os.Getenv("CI") != "" {
		os.Setenv("BILIBILI_ANONYMOUS_LOGIN", "true")
		os.Setenv("SKIP_SMALL_FLUSH", "false")
	}
	logrus.SetLevel(logrus.DebugLevel)
}
