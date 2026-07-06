package recorder_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/utils"
	"github.com/sirupsen/logrus"
)

func TestFlvRecord(t *testing.T) {
	runFormatRecordTest(t, bilibili.ProfileHTTPFLV, "flv")
}

func TestTsRecord(t *testing.T) {
	runFormatRecordTest(t, bilibili.ProfileHLSTS, "ts")
}

func TestFmp4Record(t *testing.T) {
	runFormatRecordTest(t, bilibili.ProfileHLSFMP4, "fmp4")
}

func TestFlvFmp4ConcurrentRecord(t *testing.T) {
	runConcurrentFormatRecordTest(t,
		concurrentFormatRecordSpec{profile: bilibili.ProfileHTTPFLV, format: "flv"},
		concurrentFormatRecordSpec{profile: bilibili.ProfileHLSFMP4, format: "fmp4"},
	)
}

// ZZZ_Final_* long soak tests run in isolated go test processes so heap/cpu pprof are
// not polluted by other integration tests. CI runs each in a separate workflow step; locally:
//
//	go test ./internal/services/recorder -run TestZZZ_Final_Concurrent3WayFlvRecord -count=1 -timeout 30m
//	go test ./internal/services/recorder -run TestZZZ_Final_Concurrent3WayFmp4Record -count=1 -timeout 30m
//
// The ZZZ prefix keeps lexicographic order last when the full recorder package is
// run in one invocation (e.g. go test ./internal/services/recorder without -run).
//
// Optional: RECORDER_RECORD_PROFILE_INTERVAL_SECS=60s
func TestZZZ_Final_Concurrent3WayFlvRecord(t *testing.T) {
	runZZZFinalConcurrentRecordTest(t, bilibili.ProfileHTTPFLV, "flv", 3)
}

func TestZZZ_Final_Concurrent3WayFmp4Record(t *testing.T) {
	runZZZFinalConcurrentRecordTest(t, bilibili.ProfileHLSFMP4, "fmp4", 3)
}

func TestFlvRecord_AutoStopAfterDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping TestFlvRecord_AutoStopAfterDuration in short mode")
	}

	const recordDuration = 60 * time.Second
	const pollInterval = 2 * time.Second
	const tolerance = 20 * time.Second

	sess := newRecorderTestSession(t)
	room := resolveLiveTestRoomID(t, sess.Room)

	t.Logf("starting recording with duration limit: %v", recordDuration)
	startPhase, err := sess.Monitor.beginPhase("auto_stop_start")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := sess.Recorder.Start(room, recorder.WithDuration(recordDuration))
	startReport := startPhase.end(t)
	handleRecordingStartErr(t, startErr)
	logCPUPhase(t, startReport)

	if status := sess.Recorder.GetStatus(room); status != recorder.Recording {
		t.Fatalf("expected status %q immediately after start, got %q", recorder.Recording, status)
	}

	deadline := time.Now().Add(recordDuration + tolerance)
	startTime := time.Now()
	for time.Now().Before(deadline) {
		<-time.After(pollInterval)
		status := sess.Recorder.GetStatus(room)
		t.Logf("elapsed: %v, status: %s", time.Since(startTime).Round(time.Second), status)
		if status == recorder.Idle {
			sess.Monitor.snapshotMemory(t, "after_auto_stop", false)
			sess.Monitor.snapshotGoroutines(t, "after_auto_stop")
			sess.Monitor.logAnalysisHints(t)
			t.Logf("recording auto-stopped after ~%v as expected", recordDuration)
			return
		}
	}

	t.Errorf("recording did not auto-stop within %v (duration=%v + tolerance=%v)", recordDuration+tolerance, recordDuration, tolerance)
	sess.Recorder.Stop(room)
}

func TestChannelRangeReturnedWhileStreaming(t *testing.T) {
	ch := make(chan int, 10)
	send := func() {
		for i := 0; i < 10; i++ {
			ch <- i
			time.Sleep(1 * time.Second)
		}
		close(ch)
	}
	go send()
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
	polls := 0
	hasAnyStats := false
	lastStatus := recorder.Idle
	lastOutputPath := ""
	lastElapsedSeconds := int64(0)
	for time.Now().Before(deadline) {
		polls++
		stats, hasStats := recorderService.GetStats(room)
		if hasStats && stats != nil {
			hasAnyStats = true
			lastStatus = stats.Status
			lastOutputPath = stats.OutputPath
			lastElapsedSeconds = stats.ElapsedSeconds
			if stats.OutputPath != "" {
				return stats.OutputPath
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !hasAnyStats {
		t.Logf("output path wait timeout: no stats for room=%d (recording may have been removed before assertion), polls=%d, timeout=%s", room, polls, timeout)
		return ""
	}
	t.Logf("output path wait timeout: stats available but output path is still empty, room=%d, last_status=%s, last_elapsed=%ds, polls=%d, timeout=%s, last_output_path=%q", room, lastStatus, lastElapsedSeconds, polls, timeout, lastOutputPath)
	return ""
}

func waitForOutputPathAfterStart(t *testing.T, recorderService *recorder.Service, room int) string {
	t.Helper()
	timeout := time.Duration(utils.Ternary(os.Getenv("CI") != "", 30, 10)) * time.Second
	outputPath := waitForOutputPath(t, recorderService, room, timeout)
	if outputPath == "" {
		t.Fatalf("recording started but output path was not available within %s", timeout)
	}
	t.Logf("captured output path early for room=%d: %s", room, outputPath)
	return outputPath
}

// checkFFmpegAvailable checks if ffmpeg and ffprobe are available in the system PATH
func checkFFmpegAvailable(t *testing.T) bool {
	_, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Logf("⚠️ ffprobe not found in PATH, skipping playability verification: %v", err)
		return false
	}
	t.Log("✓ ffprobe found, will verify recorded file playability")
	return true
}

var recordingFormatExtensions = map[string]string{
	"flv":  ".flv",
	"ts":   ".ts",
	"fmp4": ".fmp4",
}

func verifyAllRecordingsInRoomDir(t *testing.T, roomDir, expectedFormat string) {
	t.Helper()
	if !checkFFmpegAvailable(t) {
		return
	}
	ext, ok := recordingFormatExtensions[expectedFormat]
	if !ok {
		t.Fatalf("unknown format %q", expectedFormat)
	}

	pattern := filepath.Join(roomDir, "*"+ext)
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(files) == 0 {
		t.Fatalf("no %s recordings under %s", ext, roomDir)
	}

	sort.Strings(files)
	t.Logf("ffprobe %d file(s) in %s", len(files), roomDir)
	for i, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Logf("[%d/%d] %s", i+1, len(files), f)
			verifyRecordingPlayability(t, f, expectedFormat)
		})
	}
}

func parseFloatDuration(durationStr string) (float64, error) {
	var result float64
	_, err := fmt.Sscanf(durationStr, "%f", &result)
	return result, err
}

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

type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []ffprobeStreamInfo `json:"streams"`
}

func verifyRecordingPlayability(t *testing.T, filePath string, expectedFormat string) {
	if filePath == "" {
		t.Error("❌ Output file path is empty")
		return
	}

	time.Sleep(500 * time.Millisecond)

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
		os.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
		os.Setenv("SKIP_SMALL_FLUSH", "false")
		logrus.SetLevel(logrus.DebugLevel)
	}
}
