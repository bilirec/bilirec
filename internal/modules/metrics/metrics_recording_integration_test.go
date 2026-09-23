package metrics_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/testutil/recording"
)

const (
	seriesActiveRecordings           = "bilirec_active_recordings"
	seriesRoomRecordingActive        = "bilirec_room_recording_active"
	seriesRoomRecordingSessionsTotal = "bilirec_room_recording_sessions_total"
	seriesRoomStreamBytesTotal       = "bilirec_room_stream_bytes_total"
	seriesRoomStreamBytesWritten     = "bilirec_room_stream_bytes_written_total"
	seriesRoomStreamConnectionActive = "bilirec_room_stream_connection_active"
	seriesRoomInfo                   = "bilirec_room_info"
)

func seriesRoom(name string, roomID int) string {
	return fmt.Sprintf(`%s{room_id="%d"}`, name, roomID)
}

func scrapeSampleValue(out, seriesPrefix string) (float64, bool) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, seriesPrefix) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

func mustScrapeRoomValue(t *testing.T, out, metric string, roomID int) float64 {
	t.Helper()
	v, ok := scrapeSampleValue(out, seriesRoom(metric, roomID))
	if !ok {
		t.Fatalf("series %q for room %d not found in scrape:\n%s", metric, roomID, out)
	}
	return v
}

func waitForScrape(t *testing.T, timeout time.Duration, scrape func() string, ok func(out string) bool) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out := scrape()
		if ok(out) {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for metrics condition; last scrape:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func assertMetricsDuringRecording(t *testing.T, scrape func() string, roomID int) {
	t.Helper()

	waitForScrape(t, 3*time.Minute, scrape, func(out string) bool {
		active, activeOK := scrapeSampleValue(out, seriesActiveRecordings+" ")
		recording, recOK := scrapeSampleValue(out, seriesRoom(seriesRoomRecordingActive, roomID))
		bytes, bytesOK := scrapeSampleValue(out, seriesRoom(seriesRoomStreamBytesTotal, roomID))
		conn, connOK := scrapeSampleValue(out, seriesRoom(seriesRoomStreamConnectionActive, roomID))
		sessions, sessOK := scrapeSampleValue(out, seriesRoom(seriesRoomRecordingSessionsTotal, roomID))
		return activeOK && active == 1 &&
			recOK && recording == 1 &&
			bytesOK && bytes > 0 &&
			connOK && conn == 1 &&
			sessOK && sessions >= 1
	})
}

func assertMetricsAfterStop(t *testing.T, scrape func() string, roomID int, uname string) {
	t.Helper()

	out := waitForScrape(t, 2*time.Minute, scrape, func(out string) bool {
		active, activeOK := scrapeSampleValue(out, seriesActiveRecordings+" ")
		return activeOK && active == 0
	})

	if _, ok := scrapeSampleValue(out, seriesRoom(seriesRoomRecordingActive, roomID)); ok {
		t.Fatalf("recording_active gauge should be unregistered after stop, still in scrape:\n%s", out)
	}
	if _, ok := scrapeSampleValue(out, seriesRoom(seriesRoomStreamConnectionActive, roomID)); ok {
		t.Fatalf("stream_connection_active gauge should be unregistered after stop, still in scrape:\n%s", out)
	}

	sessions := mustScrapeRoomValue(t, out, seriesRoomRecordingSessionsTotal, roomID)
	if sessions < 1 {
		t.Fatalf("recording_sessions_total = %v, want >= 1", sessions)
	}
	streamBytes := mustScrapeRoomValue(t, out, seriesRoomStreamBytesTotal, roomID)
	if streamBytes <= 0 {
		t.Fatalf("stream_bytes_total = %v, want > 0", streamBytes)
	}
	written := mustScrapeRoomValue(t, out, seriesRoomStreamBytesWritten, roomID)
	if written <= 0 {
		t.Fatalf("stream_bytes_written_total = %v, want > 0", written)
	}

	if uname != "" {
		want := fmt.Sprintf(`%s{room_id="%d",uname=%q}`, seriesRoomInfo, roomID, uname)
		if !strings.Contains(out, want) {
			t.Fatalf("missing room_info for uname %q in scrape:\n%s", uname, out)
		}
	}
}

func runMetricsIntegrationRecordTest(t *testing.T) {
	t.Helper()

	outputDir := t.TempDir()
	t.Setenv("OUTPUT_DIR", outputDir)
	t.Setenv("METRICS_ENABLED", "true")
	t.Setenv("METRICS_PORT", "0")

	sess := recording.NewSession(t)
	if sess.Metrics == nil || !sess.Metrics.Enabled() {
		t.Fatal("METRICS_ENABLED not active in recording session")
	}
	scrape := sess.Metrics.Scrape

	roomID := recording.ResolveLiveTestRoomID(t, sess.Room)
	roomInfo, err := sess.Room.GetLiveRoomInfo(roomID)
	if err != nil {
		t.Fatalf("GetLiveRoomInfo: %v", err)
	}

	startErr := sess.Recorder.Start(roomID)
	recording.HandleRecordingStartErr(t, startErr)

	outputPath := recording.WaitForOutputPathAfterStart(t, sess.Recorder, roomID)
	assertMetricsDuringRecording(t, scrape, roomID)

	recordDuration := recording.IntegrationRecordDuration()
	t.Logf("metrics integration: recording room=%d for %s (output=%s)", roomID, recordDuration, outputPath)
	_ = sess.Monitor.RunRecordingProfiledWait(t, "metrics_recording", recordDuration)

	t.Log("stopping recording")
	if !sess.Recorder.Stop(roomID) {
		t.Fatal("stop returned false")
	}
	recording.WaitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	time.Sleep(recording.SettleAfterStop)

	uname := ""
	if roomInfo != nil {
		uname = roomInfo.Uname
	}
	assertMetricsAfterStop(t, scrape, roomID, uname)

	t.Logf("metrics integration ok: room=%d stream_bytes=%.0f written=%.0f",
		roomID,
		mustScrapeRoomValue(t, scrape(), seriesRoomStreamBytesTotal, roomID),
		mustScrapeRoomValue(t, scrape(), seriesRoomStreamBytesWritten, roomID),
	)
}

// Long-running live recording with METRICS_ENABLED and Prometheus scrape assertions.
// Isolated run (CI also runs this via ./... unless -short):
//
//	go test ./internal/modules/metrics -run TestLong_MetricsDuringRecord -count=1 -timeout 30m
func TestLong_MetricsDuringRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping metrics integration record test in short mode")
	}
	if os.Getenv("CI") != "" {
		t.Log("metrics integration: CI record window follows IntegrationRecordDuration()")
	}
	runMetricsIntegrationRecordTest(t)
}
