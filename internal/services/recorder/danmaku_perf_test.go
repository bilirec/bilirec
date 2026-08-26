package recorder_test

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/danmaku"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/utils"
)

func danmakuRecordStartOptions() []recorder.RecordStartOption {
	return []recorder.RecordStartOption{
		recorder.WithRecordDanmaku(true),
		recorder.WithStreamOptions(
			bilibili.WithProfiles(bilibili.ProfileHTTPFLV),
			bilibili.WithQn(bilibili.QualityOriginal),
		),
	}
}

func videoOnlyFLVStartOptions() []recorder.RecordStartOption {
	return []recorder.RecordStartOption{
		recorder.WithStreamOptions(
			bilibili.WithProfiles(bilibili.ProfileHTTPFLV),
			bilibili.WithQn(bilibili.QualityOriginal),
		),
	}
}

func danmakuPerfRecordDuration() time.Duration {
	return time.Duration(utils.Ternary(os.Getenv("CI") != "", 2, 1)) * time.Minute
}

func waitUntilNoDanmakuSessions(t *testing.T, svc *danmaku.Service, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if svc.ActiveSessions() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("danmaku sessions still active after %s: %d", timeout, svc.ActiveSessions())
}

func waitForDanmakuSession(t *testing.T, svc *danmaku.Service, timeout time.Duration) {
	t.Helper()
	waitForDanmakuSessions(t, svc, 1, timeout)
}

func waitForDanmakuSessions(t *testing.T, svc *danmaku.Service, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if svc.ActiveSessions() == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("danmaku sessions = %d, want %d after %s", svc.ActiveSessions(), want, timeout)
}

func startDanmakuRecording(t *testing.T, sess *recorderTestSession, room int) error {
	t.Helper()
	sess.Room.InvalidateRooms(room)
	isLive, err := sess.Room.IsRoomLive(room)
	if err != nil {
		return err
	}
	if !isLive {
		return recorder.ErrStreamNotLive
	}
	return sess.Recorder.Start(room, danmakuRecordStartOptions()...)
}

func logDanmakuRecorderMetric(t *testing.T, name string, fields map[string]any) {
	t.Helper()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	msg := name
	for _, k := range keys {
		msg += fmt.Sprintf(" %s=%v", k, fields[k])
	}
	t.Logf("METRIC %s", msg)
}

// TestDanmakuJsonlRecord_Perf mirrors TestFlvRecord / TestAudioOnlyFlvRecord:
// profiled start + recording window, heap/goroutine snapshots, and a retained
// memory budget. Sidecar recording is enabled; 0 chat lines is still a valid
// idle-session leak case and is not skipped.
func TestDanmakuJsonlRecord_Perf(t *testing.T) {
	runDanmakuProfiledRecordTest(t, "jsonl", danmakuPerfRecordDuration())
}

// TestZZZ_Final_DanmakuJsonlRecord is an isolated soak so heap/cpu pprof are
// not polluted by other integration tests. CI runs it in a separate step:
//
//	go test ./internal/services/recorder -run TestZZZ_Final_DanmakuJsonlRecord -count=1 -timeout 30m
func TestZZZ_Final_DanmakuJsonlRecord(t *testing.T) {
	runDanmakuProfiledRecordTest(t, "jsonl", integrationRecordDuration())
}

func runDanmakuProfiledRecordTest(t *testing.T, format string, recordDuration time.Duration) {
	t.Helper()
	if testing.Short() {
		t.Skipf("skipping danmaku %s perf record test in short mode", format)
	}

	t.Setenv("DANMAKU_OUTPUT_FORMAT", format)

	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	baseline := sess.Monitor.snapshotMemory(t, "danmaku_baseline", true)
	baselineG := sess.Monitor.snapshotGoroutines(t, "danmaku_baseline")

	startPhase, err := sess.Monitor.beginPhase("danmaku_" + format + "_start")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := startDanmakuRecording(t, sess, roomID)
	startReport := startPhase.end(t)
	handleRecordingStartErr(t, startErr)
	logCPUPhase(t, startReport)

	outputPath := waitForOutputPathAfterStart(t, sess.Recorder, roomID)
	waitForDanmakuSession(t, sess.Danmaku, 15*time.Second)
	sidecarPath := danmaku.PathForVideo(outputPath, "."+format)

	t.Logf("danmaku perf record: format=%s room=%d duration=%s video=%s sidecar=%s",
		format, roomID, recordDuration, outputPath, sidecarPath)

	recordReport := sess.Monitor.runRecordingProfiledWait(t, "danmaku_"+format+"_recording", recordDuration)
	during := sess.Monitor.snapshotMemory(t, "danmaku_during", false)
	duringG := sess.Monitor.snapshotGoroutines(t, "danmaku_during")
	logMemoryDelta(t, baseline, during)

	t.Logf("active danmaku sessions after soak window: %d", sess.Danmaku.ActiveSessions())
	bytesWritten := sess.Danmaku.GetBytesWritten(roomID)

	// Same as runFormatRecordTest: a live room may already have auto-stopped
	// (streamer offline past MaxRetryMinutes). Stop() returning false is not a failure.
	t.Log("stopping recording")
	t.Logf("stop success: %v", sess.Recorder.Stop(roomID))
	waitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	waitUntilNoDanmakuSessions(t, sess.Danmaku, 15*time.Second)
	time.Sleep(recorderTestSettleAfterStop)

	afterStop := sess.Monitor.snapshotMemory(t, "danmaku_after_stop", false)
	logMemoryDelta(t, during, afterStop)

	afterCleanup := sess.Monitor.snapshotMemoryReleased(t, "danmaku_"+format+"_after_cleanup")
	afterG := sess.Monitor.snapshotGoroutines(t, "danmaku_after_cleanup")
	assertRecordingMemoryReleased(t, baseline, afterCleanup, recordingMemoryBudgetForSessions(1, "danmaku_"+format))

	logDanmakuRecorderMetric(t, "danmaku_profiled_record", map[string]any{
		"format":            format,
		"duration":          recordDuration.String(),
		"start_util_pct":    fmt.Sprintf("%.1f", startReport.UtilPercent),
		"record_util_pct":   fmt.Sprintf("%.1f", recordReport.UtilPercent),
		"during_alloc_mb":   fmt.Sprintf("%.2f", during.AllocMB),
		"retained_alloc_mb": fmt.Sprintf("%.2f", memAllocDiffMB(afterCleanup, baseline)),
		"retained_sys_mb":   fmt.Sprintf("%.2f", memSysDiffMB(afterCleanup, baseline)),
		"goroutine_during":  duringG - baselineG,
		"goroutine_after":   afterG - baselineG,
		"danmaku_bytes":     bytesWritten,
		"active_after_stop": sess.Danmaku.ActiveSessions(),
	})

	switch format {
	case "jsonl":
		if !waitForFinalizedDanmakuJSONL(t, sidecarPath, 20*time.Second) {
			t.Errorf("danmaku jsonl not finalized: %s", sidecarPath)
			break
		}
		stats := parseDanmakuJSONL(t, sidecarPath)
		t.Logf("danmaku jsonl stats: d=%d sc=%d gift=%d guard=%d bytes=%d",
			stats.DanmakuCount, stats.SCCount, stats.GiftCount, stats.GuardCount, bytesWritten)
		if !stats.HasMeta {
			t.Error("danmaku jsonl missing meta line")
		}
	case "xml":
		if !waitForFinalizedDanmakuXML(t, sidecarPath, 20*time.Second) {
			t.Errorf("danmaku xml not finalized: %s", sidecarPath)
			break
		}
		stats := parseDanmakuXML(t, sidecarPath)
		t.Logf("danmaku xml stats: d=%d sc=%d gift=%d guard=%d bytes=%d",
			stats.DanmakuCount, stats.SCCount, stats.GiftCount, stats.GuardCount, bytesWritten)
	}

	sess.Monitor.logAnalysisHints(t)
}

func TestDanmakuRecord_CPUSpike(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping danmaku CPU spike test in short mode")
	}

	t.Setenv("DANMAKU_OUTPUT_FORMAT", "jsonl")
	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	steadySample := recorderCPUSteadySampleDuration()

	startPhase, err := sess.Monitor.beginPhase("danmaku_recorder_start_spike")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := startDanmakuRecording(t, sess, roomID)
	startReport := startPhase.end(t)
	handleRecordingStartErr(t, startErr)
	logCPUPhase(t, startReport)

	defer func() {
		_ = sess.Recorder.Stop(roomID)
		waitUntilNoActiveRecordings(t, sess.Recorder, 12*time.Second)
		waitUntilNoDanmakuSessions(t, sess.Danmaku, 12*time.Second)
	}()

	waitForOutputPathAfterStart(t, sess.Recorder, roomID)
	waitForDanmakuSession(t, sess.Danmaku, 15*time.Second)
	time.Sleep(200 * time.Millisecond)

	steadyPhase, err := sess.Monitor.beginPhase("danmaku_recorder_steady_state")
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
	logDanmakuRecorderMetric(t, "danmaku_cpu_spike", map[string]any{
		"start_util_pct":  fmt.Sprintf("%.1f", startReport.UtilPercent),
		"steady_util_pct": fmt.Sprintf("%.1f", steadyReport.UtilPercent),
		"steady_avg_cpu":  fmt.Sprintf("%.1f", steadyReport.AvgCPUPercent),
	})
	sess.Monitor.logAnalysisHints(t)
}

func TestDanmakuRecord_MemoryLeak_MultipleStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping danmaku multi-cycle memory test in short mode")
	}

	t.Setenv("DANMAKU_OUTPUT_FORMAT", "jsonl")
	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	baseline := sess.Monitor.snapshotMemory(t, "danmaku_cycle_baseline", true)

	const cycles = 5
	memSamples := make([]float64, cycles+1)
	memSamples[0] = baseline.AllocMB

	for cycle := 0; cycle < cycles; cycle++ {
		t.Logf("danmaku cycle %d/%d", cycle+1, cycles)
		phase, err := sess.Monitor.beginPhase(fmt.Sprintf("danmaku_cycle_%d_start", cycle+1))
		if err != nil {
			t.Fatalf("cycle %d begin phase: %v", cycle+1, err)
		}
		startErr := startDanmakuRecording(t, sess, roomID)
		report := phase.end(t)
		handleRecordingStartErr(t, startErr)
		logCPUPhase(t, report)

		waitForOutputPathAfterStart(t, sess.Recorder, roomID)
		waitForDanmakuSession(t, sess.Danmaku, 15*time.Second)
		time.Sleep(10 * time.Second)

		if !sess.Recorder.Stop(roomID) {
			t.Errorf("cycle %d: failed to stop", cycle+1)
		}
		waitUntilNoActiveRecordings(t, sess.Recorder, 12*time.Second)
		waitUntilNoDanmakuSessions(t, sess.Danmaku, 12*time.Second)
		time.Sleep(2 * time.Second)

		snap := sess.Monitor.snapshotMemory(t, fmt.Sprintf("danmaku_cycle_%d_after_gc", cycle+1), true)
		memSamples[cycle+1] = snap.AllocMB
		t.Logf("  memory after cycle %d: %.2f MB", cycle+1, memSamples[cycle+1])
	}

	final := memSamples[cycles]
	totalGrowth := final - baseline.AllocMB
	avgGrowthPerCycle := totalGrowth / float64(cycles)
	logDanmakuRecorderMetric(t, "danmaku_multi_cycle_memory", map[string]any{
		"cycles":               cycles,
		"baseline_mb":          fmt.Sprintf("%.2f", baseline.AllocMB),
		"final_mb":             fmt.Sprintf("%.2f", final),
		"total_growth_mb":      fmt.Sprintf("%.2f", totalGrowth),
		"avg_growth_per_cycle": fmt.Sprintf("%.2f", avgGrowthPerCycle),
	})

	if avgGrowthPerCycle > 5.0 {
		t.Errorf("memory growing linearly with danmaku: %.2f MB per cycle", avgGrowthPerCycle)
	}
	if totalGrowth > 25.0 {
		t.Errorf("excessive memory growth with danmaku: %.2f MB after %d cycles", totalGrowth, cycles)
	}
	sess.Monitor.logAnalysisHints(t)
}

func TestDanmakuRecord_GoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping danmaku goroutine leak test in short mode")
	}

	t.Setenv("DANMAKU_OUTPUT_FORMAT", "jsonl")
	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	time.Sleep(time.Second)
	baseline := sess.Monitor.snapshotGoroutines(t, "danmaku_goroutine_baseline")

	const cycles = 3
	for cycle := 0; cycle < cycles; cycle++ {
		t.Logf("danmaku goroutine cycle %d/%d", cycle+1, cycles)
		startErr := startDanmakuRecording(t, sess, roomID)
		handleRecordingStartErr(t, startErr)
		waitForOutputPathAfterStart(t, sess.Recorder, roomID)
		waitForDanmakuSession(t, sess.Danmaku, 15*time.Second)

		time.Sleep(8 * time.Second)
		during := sess.Monitor.snapshotGoroutines(t, fmt.Sprintf("danmaku_cycle_%d_during", cycle+1))
		t.Logf("  during recording: +%d vs baseline", during-baseline)

		sess.Recorder.Stop(roomID)
		waitUntilNoActiveRecordings(t, sess.Recorder, 12*time.Second)
		waitUntilNoDanmakuSessions(t, sess.Danmaku, 12*time.Second)
		time.Sleep(2 * time.Second)
		afterStop := sess.Monitor.snapshotGoroutines(t, fmt.Sprintf("danmaku_cycle_%d_after_stop", cycle+1))
		t.Logf("  after stop: +%d vs baseline", afterStop-baseline)
	}

	time.Sleep(2 * time.Second)
	final := sess.Monitor.snapshotGoroutines(t, "danmaku_goroutine_final")
	growth := final - baseline
	logDanmakuRecorderMetric(t, "danmaku_goroutine_leak", map[string]any{
		"cycles":   cycles,
		"baseline": baseline,
		"final":    final,
		"growth":   growth,
	})
	if growth > 10 {
		t.Errorf("possible danmaku goroutine leak: %d goroutines not cleaned up", growth)
	}
	sess.Monitor.logAnalysisHints(t)
}

// TestDanmakuRecord_DeltaVsVideoOnly records the same live room twice and
// attributes extra CPU/memory/goroutines to the danmaku sidecar. CPU delta is
// logged as a metric; only retained heap and leftover goroutines fail the test.
func TestDanmakuRecord_DeltaVsVideoOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping danmaku delta test in short mode")
	}

	t.Setenv("DANMAKU_OUTPUT_FORMAT", "jsonl")
	sess := newRecorderTestSession(t)
	roomID := resolveLiveTestRoomID(t, sess.Room)

	const window = 20 * time.Second

	measure := func(label string, withDanmaku bool) (cpuPhaseReport, float64, int) {
		t.Helper()
		opts := videoOnlyFLVStartOptions()
		if withDanmaku {
			opts = danmakuRecordStartOptions()
		}

		baseline := sess.Monitor.snapshotMemory(t, label+"_baseline", true)
		baselineG := sess.Monitor.snapshotGoroutines(t, label+"_baseline")
		sess.Room.InvalidateRooms(roomID)
		startErr := sess.Recorder.Start(roomID, opts...)
		handleRecordingStartErr(t, startErr)
		waitForOutputPathAfterStart(t, sess.Recorder, roomID)
		if withDanmaku {
			waitForDanmakuSession(t, sess.Danmaku, 15*time.Second)
		} else if n := sess.Danmaku.ActiveSessions(); n != 0 {
			t.Errorf("%s: danmaku disabled but %d active session(s)", label, n)
		}

		phase, err := sess.Monitor.beginPhase(label + "_steady")
		if err != nil {
			t.Fatalf("%s begin phase: %v", label, err)
		}
		time.Sleep(window)
		report := phase.end(t)
		logCPUPhase(t, report)

		sess.Recorder.Stop(roomID)
		waitUntilNoActiveRecordings(t, sess.Recorder, 12*time.Second)
		waitUntilNoDanmakuSessions(t, sess.Danmaku, 12*time.Second)
		time.Sleep(recorderTestSettleAfterStop)

		after := sess.Monitor.snapshotMemoryReleased(t, label+"_after_cleanup")
		goroutines := sess.Monitor.snapshotGoroutines(t, label+"_after_cleanup")
		retained := memAllocDiffMB(after, baseline)
		gGrowth := goroutines - baselineG
		logMemoryDelta(t, baseline, after)
		t.Logf("%s retained alloc=%+.2f MB goroutine_growth=%+d util=%.1f%%",
			label, retained, gGrowth, report.UtilPercent)
		return report, retained, gGrowth
	}

	videoCPU, videoRetained, videoG := measure("video_only", false)
	danmakuCPU, danmakuRetained, danmakuG := measure("video_plus_danmaku", true)

	extraAlloc := danmakuRetained - videoRetained
	extraG := danmakuG - videoG
	extraUtil := danmakuCPU.UtilPercent - videoCPU.UtilPercent

	logDanmakuRecorderMetric(t, "danmaku_delta_vs_video_only", map[string]any{
		"window":           window.String(),
		"video_util_pct":   fmt.Sprintf("%.1f", videoCPU.UtilPercent),
		"danmaku_util_pct": fmt.Sprintf("%.1f", danmakuCPU.UtilPercent),
		"extra_util_pct":   fmt.Sprintf("%.1f", extraUtil),
		"extra_alloc_mb":   fmt.Sprintf("%.2f", extraAlloc),
		"extra_goroutines": extraG,
	})

	const (
		maxExtraAllocMB   = 12.0
		maxExtraGoroutine = 8
	)
	if extraAlloc > maxExtraAllocMB {
		t.Errorf("danmaku sidecar retained %+.2f MB more than video-only after cleanup (limit %.2f)",
			extraAlloc, maxExtraAllocMB)
	}
	if extraG > maxExtraGoroutine {
		t.Errorf("danmaku sidecar left %+d goroutines vs video-only after cleanup (limit %d)",
			extraG, maxExtraGoroutine)
	}
	sess.Monitor.logAnalysisHints(t)
}

func TestDanmakuXmlRecord_Perf(t *testing.T) {
	runDanmakuProfiledRecordTest(t, "xml", danmakuPerfRecordDuration())
}

const danmakuConcurrentRooms = 3

// TestDanmakuJsonlConcurrent3Way_Perf records three live rooms with FLV + JSONL
// danmaku sidecars at the same time. Local default is 1 minute; CI uses 2.
func TestDanmakuJsonlConcurrent3Way_Perf(t *testing.T) {
	runDanmakuConcurrentRecordTest(t, danmakuConcurrentRooms, danmakuPerfRecordDuration())
}

// TestZZZ_Final_Concurrent3WayDanmakuJsonlRecord is the isolated soak counterpart.
// CI:
//
//	go test ./internal/services/recorder -run TestZZZ_Final_Concurrent3WayDanmakuJsonlRecord -count=1 -timeout 30m
func TestZZZ_Final_Concurrent3WayDanmakuJsonlRecord(t *testing.T) {
	runDanmakuConcurrentRecordTest(t, danmakuConcurrentRooms, integrationRecordDuration())
}

func runDanmakuConcurrentRecordTest(t *testing.T, concurrent int, recordDuration time.Duration) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping concurrent danmaku record test in short mode")
	}
	if concurrent < 1 {
		t.Fatalf("concurrent must be >= 1, got %d", concurrent)
	}

	t.Setenv("DANMAKU_OUTPUT_FORMAT", "jsonl")
	t.Setenv("MAX_CONCURRENT_RECORDINGS", strconv.Itoa(concurrent))

	label := fmt.Sprintf("concurrent%d_danmaku_jsonl", concurrent)
	sess := newRecorderTestSession(t)
	rooms := resolveLiveTestRoomIDs(t, sess.Room, concurrent)
	if len(rooms) < concurrent {
		t.Skipf("need %d live rooms, got %d", concurrent, len(rooms))
	}
	rooms = rooms[:concurrent]

	baseline := sess.Monitor.snapshotMemory(t, label+"_baseline", true)
	baselineG := sess.Monitor.snapshotGoroutines(t, label+"_baseline")
	sess.Room.InvalidateRooms(rooms...)

	t.Logf("concurrent danmaku jsonl: rooms=%v duration=%s", rooms, recordDuration)

	startPhase, err := sess.Monitor.beginPhase(label + "_start_burst")
	if err != nil {
		t.Fatalf("begin concurrent start phase: %v", err)
	}

	startGate := make(chan struct{})
	resultCh := make(chan concurrentStartResult, concurrent)
	var wg sync.WaitGroup
	for _, roomID := range rooms {
		rid := roomID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			err := sess.Recorder.Start(rid, danmakuRecordStartOptions()...)
			resultCh <- concurrentStartResult{room: rid, err: err}
		}()
	}
	close(startGate)
	wg.Wait()
	close(resultCh)

	startReport := startPhase.end(t)
	logCPUPhase(t, startReport)

	started := collectConcurrentStartResults(t, sess.Recorder, resultCh)
	if len(started) != concurrent {
		t.Fatalf("expected %d successful starts, got %d", concurrent, len(started))
	}
	if active := sess.Recorder.ListRecordingSize(); active != concurrent {
		t.Fatalf("expected %d active recordings, got %d", concurrent, active)
	}

	outputPaths := make(map[int]string, concurrent)
	for _, rid := range started {
		outputPaths[rid] = waitForOutputPathAfterStart(t, sess.Recorder, rid)
	}
	waitForDanmakuSessions(t, sess.Danmaku, concurrent, 20*time.Second)

	recordReport := sess.Monitor.runRecordingProfiledWait(t, label+"_recording", recordDuration)
	during := sess.Monitor.snapshotMemory(t, label+"_during", false)
	duringG := sess.Monitor.snapshotGoroutines(t, label+"_during")
	logCPUPhase(t, recordReport)
	logMemoryDelta(t, baseline, during)

	t.Logf("active danmaku sessions after soak window: %d (started=%d)", sess.Danmaku.ActiveSessions(), concurrent)

	bytesByRoom := make(map[int]uint64, concurrent)
	droppedByRoom := make(map[int]uint64, concurrent)
	var totalBytes, totalDropped uint64
	for _, rid := range started {
		bytesByRoom[rid] = sess.Danmaku.GetBytesWritten(rid)
		droppedByRoom[rid] = sess.Danmaku.GetDropped(rid)
		totalBytes += bytesByRoom[rid]
		totalDropped += droppedByRoom[rid]
		t.Logf("room %d danmaku bytes=%d dropped=%d video=%s",
			rid, bytesByRoom[rid], droppedByRoom[rid], outputPaths[rid])
	}

	// Same as runZZZFinalConcurrentRecordTest: Stop() false means the room
	// already auto-stopped during the soak window.
	t.Log("stopping concurrent danmaku recordings")
	for _, rid := range started {
		if !sess.Recorder.Stop(rid) {
			t.Logf("stop returned false for room=%d", rid)
		}
	}
	waitUntilNoActiveRecordings(t, sess.Recorder, 30*time.Second)
	waitUntilNoDanmakuSessions(t, sess.Danmaku, 15*time.Second)
	time.Sleep(recorderTestSettleAfterStop)

	afterStop := sess.Monitor.snapshotMemory(t, label+"_after_stop", false)
	logMemoryDelta(t, during, afterStop)

	afterCleanup := sess.Monitor.snapshotMemoryReleased(t, label+"_after_cleanup")
	afterG := sess.Monitor.snapshotGoroutines(t, label+"_after_cleanup")
	assertRecordingMemoryReleased(t, baseline, afterCleanup, recordingMemoryBudgetForSessions(concurrent, label))

	totalDanmaku := 0
	for _, rid := range started {
		sidecarPath := danmaku.PathForVideo(outputPaths[rid], ".jsonl")
		if !waitForFinalizedDanmakuJSONL(t, sidecarPath, 20*time.Second) {
			t.Errorf("danmaku jsonl not finalized: room=%d path=%s", rid, sidecarPath)
			continue
		}
		stats := parseDanmakuJSONL(t, sidecarPath)
		totalDanmaku += stats.DanmakuCount
		t.Logf("room %d jsonl: d=%d sc=%d gift=%d guard=%d meta=%v",
			rid, stats.DanmakuCount, stats.SCCount, stats.GiftCount, stats.GuardCount, stats.HasMeta)
		if !stats.HasMeta {
			t.Errorf("room %d danmaku jsonl missing meta line", rid)
		}
	}

	logDanmakuRecorderMetric(t, "danmaku_concurrent_record", map[string]any{
		"rooms":             concurrent,
		"duration":          recordDuration.String(),
		"start_util_pct":    fmt.Sprintf("%.1f", startReport.UtilPercent),
		"record_util_pct":   fmt.Sprintf("%.1f", recordReport.UtilPercent),
		"during_alloc_mb":   fmt.Sprintf("%.2f", during.AllocMB),
		"retained_alloc_mb": fmt.Sprintf("%.2f", memAllocDiffMB(afterCleanup, baseline)),
		"retained_sys_mb":   fmt.Sprintf("%.2f", memSysDiffMB(afterCleanup, baseline)),
		"goroutine_during":  duringG - baselineG,
		"goroutine_after":   afterG - baselineG,
		"danmaku_bytes":     totalBytes,
		"danmaku_dropped":   totalDropped,
		"danmaku_count":     totalDanmaku,
		"active_after_stop": sess.Danmaku.ActiveSessions(),
	})

	if afterG-baselineG > 10 {
		t.Errorf("possible goroutine leak after %d-way danmaku stop: %+d vs baseline",
			concurrent, afterG-baselineG)
	}
	sess.Monitor.logAnalysisHints(t)
}
