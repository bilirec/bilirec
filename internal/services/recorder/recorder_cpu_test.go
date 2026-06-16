package recorder_test

import (
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/recorder"
)

// TestRecorder_Start_CPUSpike_FLV1080p profiles CPU during recorder.Start() and compares
// it with a short steady-state window afterward.
//
// Run manually (requires live room + Bilibili login):
//
//	go test ./internal/services/recorder -run TestRecorder_Start_CPUSpike_FLV1080p -count=1 -timeout 5m
func TestRecorder_Start_CPUSpike_FLV1080p(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CPU spike test in short mode")
	}

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)
	sess.Room.InvalidateRooms(roomID)

	startOptions := []recorder.RecordStartOption{
		recorder.WithStreamOptions(
			bilibili.WithProfiles(bilibili.ProfileHTTPFLV),
			bilibili.WithQn(bilibili.QualityOriginal),
		),
	}

	steadySample := recorderCPUSteadySampleDuration()

	startPhase, err := sess.Monitor.beginPhase("recorder_start_spike")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := sess.Recorder.Start(roomID, startOptions...)
	startReport := startPhase.end(t)
	handleRecordingStartErr(t, startErr)
	logCPUPhase(t, startReport)

	defer func() {
		_ = sess.Recorder.Stop(roomID)
		waitUntilNoActiveRecordings(t, sess.Recorder, 12*time.Second)
	}()

	time.Sleep(200 * time.Millisecond)

	steadyPhase, err := sess.Monitor.beginPhase("recorder_steady_state")
	if err != nil {
		t.Fatalf("begin steady phase: %v", err)
	}
	avgCPU := sess.Monitor.measureAvgCPU(t, steadySample)
	steadyReport := steadyPhase.end(t)
	steadyReport.AvgCPUPercent = avgCPU
	logCPUPhase(t, steadyReport)

	if startReport.UtilPercent > 0 && steadyReport.UtilPercent > 0 {
		t.Logf("start/steady util ratio: %.2fx", startReport.UtilPercent/steadyReport.UtilPercent)
	}
	if startReport.UtilPercent > 0 && steadyReport.AvgCPUPercent > 0 {
		t.Logf("start util vs steady avg_cpu: %.1f%% vs %.1f%%", startReport.UtilPercent, steadyReport.AvgCPUPercent)
	}

	sess.Monitor.logAnalysisHints(t)
}

func TestRecorder_Start_CPUSpike_ColdVsWarm(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CPU cold/warm test in short mode")
	}

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)
	startOptions := []recorder.RecordStartOption{
		recorder.WithStreamOptions(
			bilibili.WithProfiles(bilibili.ProfileHTTPFLV),
			bilibili.WithQn(bilibili.QualityOriginal),
		),
	}

	measure := func(label string) cpuPhaseReport {
		sess.Room.InvalidateRooms(roomID)

		phase, err := sess.Monitor.beginPhase(label)
		if err != nil {
			t.Fatalf("%s begin phase: %v", label, err)
		}

		startErr := sess.Recorder.Start(roomID, startOptions...)
		report := phase.end(t)
		handleRecordingStartErr(t, startErr)
		logCPUPhase(t, report)

		time.Sleep(500 * time.Millisecond)
		if !sess.Recorder.Stop(roomID) {
			t.Logf("%s Stop returned false (recording may already be gone)", label)
		}
		waitUntilNoActiveRecordings(t, sess.Recorder, 12*time.Second)
		time.Sleep(300 * time.Millisecond)
		return report
	}

	cold := measure("cold_start")
	warm := measure("warm_start")

	t.Logf("cold wall=%s util=%.1f%%", cold.Wall, cold.UtilPercent)
	t.Logf("warm wall=%s util=%.1f%%", warm.Wall, warm.UtilPercent)
	if warm.UtilPercent > 0 {
		t.Logf("cold/warm util ratio: %.2fx", cold.UtilPercent/warm.UtilPercent)
	}
	sess.Monitor.logAnalysisHints(t)
}
