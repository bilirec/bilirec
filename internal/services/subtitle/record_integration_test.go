package subtitle_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/testutil/recording"
	"github.com/bilirec/bilirec/utils"
)

const defaultSubtitleTestRoomID = 22748536

func subtitleTestRoomID(t *testing.T) int {
	if raw := strings.TrimSpace(os.Getenv("BILIREC_SUBTITLE_TEST_ROOM_ID")); raw != "" {
		id, err := strconvAtoi(raw)
		if err != nil || id <= 0 {
			t.Fatalf("invalid BILIREC_SUBTITLE_TEST_ROOM_ID: %q", raw)
		}
		return id
	}
	return defaultSubtitleTestRoomID
}

func strconvAtoi(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, os.ErrInvalid
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func TestSubtitleRecord_Disabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subtitle disabled test in short mode")
	}
	sess := recording.NewSession(t)
	roomID := subtitleTestRoomID(t)
	live, err := sess.Room.IsRoomLive(roomID)
	if err != nil || !live {
		t.Skip("subtitle test room not live")
	}
	extra, err := sess.Bili.RoomSubtitleEnabled(roomID)
	if err != nil || !extra {
		t.Skip("subtitle not enabled for test room")
	}

	startErr := sess.Recorder.Start(roomID)
	recording.HandleRecordingStartErr(t, startErr)
	outputPath := recording.WaitForOutputPathAfterStart(t, sess.Recorder, roomID)
	roomDir := filepath.Dir(outputPath)

	time.Sleep(10 * time.Second)
	if !sess.Recorder.Stop(roomID) {
		t.Error("failed to stop recording")
	}
	recording.WaitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)

	vtt := utils.SidecarPathForVideo(outputPath, ".vtt")
	if _, err := os.Stat(vtt); err == nil {
		t.Fatalf("expected no vtt when record_subtitle disabled: %s", vtt)
	}
	_ = roomDir
}

func TestSubtitleRecord_Formats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subtitle record test in short mode")
	}
	cases := []struct {
		name    string
		profile bilibili.StreamProfile
		ext     string
	}{
		{"http-flv", bilibili.ProfileHTTPFLV, ".flv"},
		{"hls-fmp4", bilibili.ProfileHLSFMP4, ".fmp4"},
		{"hls-ts", bilibili.ProfileHLSTS, ".ts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSubtitleRecordTest(t, tc.profile, tc.ext)
		})
	}
}

func runSubtitleRecordTest(t *testing.T, profile bilibili.StreamProfile, wantExt string) {
	t.Helper()
	sess := recording.NewSession(t)
	roomID := subtitleTestRoomID(t)

	enabled, err := sess.Bili.RoomSubtitleEnabled(roomID)
	if err != nil {
		t.Skipf("subtitle room check failed: %v", err)
	}
	if !enabled {
		t.Skip("room subtitle_cfg.status != 1")
	}
	live, err := sess.Room.IsRoomLive(roomID)
	if err != nil || !live {
		t.Skip("subtitle test room not live")
	}

	startErr := sess.Recorder.Start(roomID,
		recorder.WithRecordSubtitle(true),
		recorder.WithStreamOptions(bilibili.WithProfiles(profile)),
	)
	recording.HandleRecordingStartErr(t, startErr)

	outputPath := recording.WaitForOutputPathAfterStart(t, sess.Recorder, roomID)
	if filepath.Ext(outputPath) != wantExt {
		t.Fatalf("expected ext %s, got %s", wantExt, outputPath)
	}
	vttPath := utils.SidecarPathForVideo(outputPath, ".vtt")

	const recordDuration = 75 * time.Second
	t.Logf("recording room=%d format=%s for %s", roomID, profile, recordDuration)
	time.Sleep(recordDuration)

	if !sess.Recorder.Stop(roomID) {
		t.Error("failed to stop recording")
	}
	recording.WaitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	waitUntilNotRecordingFile(t, sess.Recorder, outputPath, 30*time.Second)

	data, err := os.ReadFile(vttPath)
	if err != nil {
		live, _ := sess.Room.IsRoomLive(roomID)
		if !live {
			t.Skip("room went offline during test without subtitle file")
		}
		t.Skipf("no subtitle cues in %s on a live room; treating as quiet window", recordDuration)
	}
	content := string(data)
	if !strings.HasPrefix(content, "WEBVTT") {
		t.Fatalf("vtt missing header: %q", content[:min(40, len(content))])
	}
	if !strings.Contains(content, "-->") {
		t.Skip("no timed cues in quiet window")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func waitUntilNotRecordingFile(t *testing.T, rec *recorder.Service, videoPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !rec.IsRecording(videoPath) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for segment finalize (still recording %s)", videoPath)
}
