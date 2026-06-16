package recorder_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/recorder"
)

const recorderMemLeakStreamProfileEnv = "RECORDER_MEMLEAK_STREAM_PROFILE"

func memLeakStartOptionsFromEnv() ([]recorder.RecordStartOption, string, error) {
	raw := strings.TrimSpace(os.Getenv(recorderMemLeakStreamProfileEnv))
	if raw == "" || strings.EqualFold(raw, "auto") {
		return nil, "", nil
	}

	var profile bilibili.StreamProfile
	switch strings.ToLower(raw) {
	case string(bilibili.ProfileHTTPFLV), "httpflv", "flv":
		profile = bilibili.ProfileHTTPFLV
	case string(bilibili.ProfileHLSTS), "hlsts", "ts":
		profile = bilibili.ProfileHLSTS
	case string(bilibili.ProfileHLSFMP4), "hlsfmp4", "fmp4":
		profile = bilibili.ProfileHLSFMP4
	default:
		return nil, "", fmt.Errorf("invalid %s value %q", recorderMemLeakStreamProfileEnv, raw)
	}

	return []recorder.RecordStartOption{
		recorder.WithStreamOptions(
			bilibili.WithProfiles(profile),
			bilibili.WithQn(bilibili.QualityOriginal),
		),
	}, string(profile), nil
}

func startMemLeakRecording(t *testing.T, sess *recorderTestSession, room int) error {
	startOptions, profile, err := memLeakStartOptionsFromEnv()
	if err != nil {
		return err
	}
	if profile != "" {
		t.Logf("using stream profile from %s: %s", recorderMemLeakStreamProfileEnv, profile)
	}
	sess.Room.InvalidateRooms(room)
	isLive, err := sess.Room.IsRoomLive(room)
	if err != nil {
		return err
	}
	if !isLive {
		return recorder.ErrStreamNotLive
	}
	return sess.Recorder.Start(room, startOptions...)
}

func TestRecorder_MemoryLeak_SingleSession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping recorder memory test in short mode")
	}

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	baseline := sess.Monitor.snapshotMemory(t, "baseline", true)

	t.Log("📝 Starting recording session...")
	startPhase, err := sess.Monitor.beginPhase("memleak_start")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := startMemLeakRecording(t, sess, roomID)
	startReport := startPhase.end(t)
	handleRecordingStartErr(t, startErr)
	logCPUPhase(t, startReport)

	recordDuration := 30 * time.Second
	recordPhase, err := sess.Monitor.beginPhase("memleak_recording")
	if err != nil {
		t.Fatalf("begin recording phase: %v", err)
	}
	t.Logf("⏱️  Recording for %v...", recordDuration)
	time.Sleep(recordDuration)
	during := sess.Monitor.snapshotMemory(t, "during_recording", false)
	recordReport := recordPhase.end(t)
	logCPUPhase(t, recordReport)

	t.Log("🛑 Stopping recording...")
	if !sess.Recorder.Stop(roomID) {
		t.Error("Failed to stop recording")
	}
	time.Sleep(3 * time.Second)

	afterStop := sess.Monitor.snapshotMemory(t, "after_stop", false)
	t.Log("🧹 Running garbage collection...")
	afterGC := sess.Monitor.snapshotMemory(t, "after_gc", true)

	t.Logf("📊 Memory Analysis:")
	t.Logf("  Baseline:        %.2f MB", baseline.AllocMB)
	t.Logf("  During record:   %.2f MB (growth: %+.2f MB)", during.AllocMB, memAllocDiffMB(during, baseline))
	t.Logf("  After stop:      %.2f MB (retained: %+.2f MB)", afterStop.AllocMB, memAllocDiffMB(afterStop, baseline))
	t.Logf("  After GC:        %.2f MB (retained: %+.2f MB)", afterGC.AllocMB, memAllocDiffMB(afterGC, baseline))
	t.Logf("  Cleanup:         %.2f MB reclaimed", afterStop.AllocMB-afterGC.AllocMB)

	const (
		maxRetainedAfterStop = 30.0
		maxRetainedAfterGC   = 15.0
	)

	retainedAfterStop := memAllocDiffMB(afterStop, baseline)
	retainedAfterGC := memAllocDiffMB(afterGC, baseline)

	if retainedAfterStop > maxRetainedAfterStop {
		t.Logf("⚠️  Warning: high memory after stop: %.2f MB retained (threshold: %.2f MB)",
			retainedAfterStop, maxRetainedAfterStop)
	}

	if retainedAfterGC > maxRetainedAfterGC {
		t.Errorf("⚠️  Possible memory leak: %.2f MB retained after GC (threshold: %.2f MB)",
			retainedAfterGC, maxRetainedAfterGC)
	} else {
		t.Logf("✅ Memory properly cleaned up")
	}

	cleanupEfficiency := (afterStop.AllocMB - afterGC.AllocMB) / (during.AllocMB - baseline.AllocMB) * 100
	t.Logf("📈 Cleanup efficiency: %.1f%%", cleanupEfficiency)
	sess.Monitor.logAnalysisHints(t)
}

func TestRecorder_MemoryLeak_MultipleStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping multiple session test in short mode")
	}

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	baseline := sess.Monitor.snapshotMemory(t, "baseline", true)

	const cycles = 5
	t.Logf("🔄 Testing %d record/stop cycles...", cycles)

	memSamples := make([]float64, cycles+1)
	memSamples[0] = baseline.AllocMB

	for cycle := 0; cycle < cycles; cycle++ {
		t.Logf("Cycle %d/%d", cycle+1, cycles)

		phase, err := sess.Monitor.beginPhase(fmt.Sprintf("cycle_%d_start", cycle+1))
		if err != nil {
			t.Fatalf("cycle %d begin phase: %v", cycle+1, err)
		}
		startErr := startMemLeakRecording(t, sess, roomID)
		report := phase.end(t)
		handleRecordingStartErr(t, startErr)
		logCPUPhase(t, report)

		time.Sleep(10 * time.Second)

		if !sess.Recorder.Stop(roomID) {
			t.Errorf("Cycle %d: Failed to stop", cycle+1)
		}
		time.Sleep(2 * time.Second)

		snap := sess.Monitor.snapshotMemory(t, fmt.Sprintf("cycle_%d_after_gc", cycle+1), true)
		memSamples[cycle+1] = snap.AllocMB
		t.Logf("  Memory after cycle %d: %.2f MB", cycle+1, memSamples[cycle+1])
	}

	final := memSamples[cycles]
	totalGrowth := final - baseline.AllocMB
	avgGrowthPerCycle := totalGrowth / float64(cycles)

	t.Logf("📊 Multi-Cycle Analysis:")
	t.Logf("  Baseline:             %.2f MB", baseline.AllocMB)
	t.Logf("  After %d cycles:      %.2f MB", cycles, final)
	t.Logf("  Total growth:         %.2f MB", totalGrowth)
	t.Logf("  Avg growth per cycle: %.2f MB", avgGrowthPerCycle)

	if avgGrowthPerCycle > 5.0 {
		t.Errorf("⚠️  Memory growing linearly: %.2f MB per cycle (possible leak)", avgGrowthPerCycle)
	} else {
		t.Logf("✅ Memory growth acceptable")
	}

	if totalGrowth > 25.0 {
		t.Errorf("⚠️  Excessive memory growth: %.2f MB after %d cycles", totalGrowth, cycles)
	}
	sess.Monitor.logAnalysisHints(t)
}

func TestRecorder_MemoryLeak_ProcessedDataCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	baseline := sess.Monitor.snapshotMemory(t, "baseline", true)

	startPhase, err := sess.Monitor.beginPhase("processed_data_start")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := startMemLeakRecording(t, sess, roomID)
	startReport := startPhase.end(t)
	handleRecordingStartErr(t, startErr)
	logCPUPhase(t, startReport)

	t.Log("⏱️  Recording to test processedData cleanup...")
	recordPhase, err := sess.Monitor.beginPhase("processed_data_recording")
	if err != nil {
		t.Fatalf("begin recording phase: %v", err)
	}
	time.Sleep(20 * time.Second)
	_ = recordPhase.end(t)

	sess.Recorder.Stop(roomID)
	time.Sleep(1 * time.Second)
	afterCleanup := sess.Monitor.snapshotMemory(t, "after_cleanup", true)

	retained := memAllocDiffMB(afterCleanup, baseline)
	t.Logf("📊 ProcessedData Cleanup Test:")
	t.Logf("  Baseline:       %.2f MB", baseline.AllocMB)
	t.Logf("  After cleanup:  %.2f MB", afterCleanup.AllocMB)
	t.Logf("  Retained:       %.2f MB", retained)

	if retained > 12.0 {
		t.Errorf("⚠️  ProcessedData may not be cleaned up properly: %.2f MB retained", retained)
	} else {
		t.Logf("✅ ProcessedData properly cleared by GC")
	}
	sess.Monitor.logAnalysisHints(t)
}

func TestRecorder_MemoryLeak_ConcurrentRecordings(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent test in short mode")
	}

	sess := newRecorderTestSession(t)
	testRooms := resolveLiveTestRoomIDs(t, sess.Room, 2)

	baseline := sess.Monitor.snapshotMemory(t, "baseline", true)
	t.Logf("🔀 Testing concurrent recordings for %d rooms", len(testRooms))

	startedRooms := []int{}
	for _, room := range testRooms {
		err := startMemLeakRecording(t, sess, room)
		if err != nil {
			switch err {
			case recorder.ErrStreamNotLive:
				t.Skip("stream not live")
			case recorder.ErrEmptyStreamURLs:
				t.Skip("no stream URLs available")
			case recorder.ErrStreamURLsUnreachable:
				t.Skip("stream URLs unreachable")
			}
			t.Logf("Failed to start room %d: %v", room, err)
			continue
		}
		startedRooms = append(startedRooms, room)
		t.Logf("Started recording room %d", room)
	}

	if len(startedRooms) == 0 {
		t.Skip("No rooms available for testing")
	}

	recordPhase, err := sess.Monitor.beginPhase("concurrent_recording")
	if err != nil {
		t.Fatalf("begin recording phase: %v", err)
	}
	time.Sleep(20 * time.Second)
	during := sess.Monitor.snapshotMemory(t, "during_concurrent", false)
	_ = recordPhase.end(t)

	for _, room := range startedRooms {
		sess.Recorder.Stop(room)
		t.Logf("Stopped recording room %d", room)
	}

	time.Sleep(3 * time.Second)
	afterCleanup := sess.Monitor.snapshotMemory(t, "after_cleanup", true)

	t.Logf("📊 Concurrent Recording Analysis:")
	t.Logf("  Baseline:         %.2f MB", baseline.AllocMB)
	t.Logf("  During recording: %.2f MB", during.AllocMB)
	t.Logf("  After cleanup:    %.2f MB", afterCleanup.AllocMB)
	t.Logf("  Retained:         %.2f MB", memAllocDiffMB(afterCleanup, baseline))

	maxRetained := 20.0 * float64(len(startedRooms))
	retained := memAllocDiffMB(afterCleanup, baseline)
	if retained > maxRetained {
		t.Errorf("⚠️  Possible leak in concurrent scenario: %.2f MB retained (threshold: %.2f MB)",
			retained, maxRetained)
	} else {
		t.Logf("✅ Concurrent recordings cleaned up properly")
	}
	sess.Monitor.logAnalysisHints(t)
}

func TestRecorder_Goroutine_Leak(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping goroutine leak test in short mode")
	}

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	time.Sleep(1 * time.Second)
	baseline := sess.Monitor.snapshotGoroutines(t, "baseline")

	const cycles = 3
	for cycle := 0; cycle < cycles; cycle++ {
		t.Logf("Cycle %d/%d", cycle+1, cycles)

		phase, err := sess.Monitor.beginPhase(fmt.Sprintf("goroutine_cycle_%d", cycle+1))
		if err != nil {
			t.Fatalf("cycle %d begin phase: %v", cycle+1, err)
		}
		startErr := startMemLeakRecording(t, sess, roomID)
		_ = phase.end(t)
		handleRecordingStartErr(t, startErr)

		time.Sleep(8 * time.Second)
		during := sess.Monitor.snapshotGoroutines(t, fmt.Sprintf("cycle_%d_during", cycle+1))
		t.Logf("  During recording: +%d vs baseline", during-baseline)

		sess.Recorder.Stop(roomID)
		time.Sleep(2 * time.Second)
		afterStop := sess.Monitor.snapshotGoroutines(t, fmt.Sprintf("cycle_%d_after_stop", cycle+1))
		t.Logf("  After stop: +%d vs baseline", afterStop-baseline)
	}

	time.Sleep(2 * time.Second)
	final := sess.Monitor.snapshotGoroutines(t, "final")
	growth := final - baseline

	t.Logf("📊 Goroutine Analysis:")
	t.Logf("  Baseline:  %d", baseline)
	t.Logf("  Final:     %d", final)
	t.Logf("  Growth:   +%d", growth)

	if growth > 10 {
		t.Errorf("⚠️  Possible goroutine leak: %d goroutines not cleaned up", growth)
	} else {
		t.Logf("✅ No goroutine leak detected")
	}
	sess.Monitor.logAnalysisHints(t)
}
