package recorder_test

import (
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/services/recorder"
)

// TestRecorder_MaxConcurrentRecordingsRace_Guard ensures concurrent starts never
// exceed MAX_CONCURRENT_RECORDINGS. Any overflow indicates a regression.
func TestRecorder_MaxConcurrentRecordingsRace_Guard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping race repro in short mode")
	}

	t.Setenv("MAX_CONCURRENT_RECORDINGS", "3")

	sess := newRecorderTestSession(t)
	rooms := resolveLiveTestRoomIDs(t, sess.Room, 4)
	rounds := 20
	const (
		settleDelay    = 200 * time.Millisecond
		cleanupTimeout = 12 * time.Second
	)

	for round := 1; round <= rounds; round++ {
		sess.Room.InvalidateRooms(rooms...)

		roundPhase, err := sess.Monitor.beginPhase("concurrent_race_round")
		if err != nil {
			t.Fatalf("round %d begin phase: %v", round, err)
		}

		startGate := make(chan struct{})
		resultCh := make(chan error, len(rooms))
		var wg sync.WaitGroup

		for _, roomID := range rooms {
			rid := roomID
			wg.Go(func() {
				<-startGate
				err := sess.Recorder.Start(rid)
				if err == nil {
					t.Logf("%v 房間錄製啓動開始", rid)
				} else {
					t.Logf("%v 房間錄製啓動失敗: %v", rid, err)
				}
				resultCh <- err
			})
		}

		close(startGate)
		wg.Wait()
		close(resultCh)

		success := 0
		maxLimitErr := 0
		otherErr := 0
		for err := range resultCh {
			switch err {
			case nil:
				success++
			case recorder.ErrMaxConcurrentRecordingsReached:
				maxLimitErr++
			default:
				otherErr++
			}
		}

		time.Sleep(settleDelay)
		active := sess.Recorder.ListRecordingSize()
		roundReport := roundPhase.end(t)
		logCPUPhase(t, roundReport)
		sess.Monitor.snapshotGoroutines(t, "after_round")

		if active == 4 {
			t.Fatalf("round %d overflow detected: success=%d active=%d max_limit_err=%d other_err=%d", round, success, active, maxLimitErr, otherErr)
		}
		t.Logf("round %d: success=%d active=%d max_limit_err=%d other_err=%d", round, success, active, maxLimitErr, otherErr)

		for _, roomID := range rooms {
			sess.Recorder.Stop(roomID)
		}
		waitUntilNoActiveRecordings(t, sess.Recorder, cleanupTimeout)
	}

	sess.Monitor.logAnalysisHints(t)
}

func waitUntilNoActiveRecordings(t *testing.T, recorderService *recorder.Service, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if recorderService.ListRecordingSize() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("cleanup timeout: still has %d active recordings", recorderService.ListRecordingSize())
}

type concurrentStartResult struct {
	room int
	err  error
}

// collectConcurrentStartResults drains all concurrent Start results before handling
// errors so a failure received early cannot skip Stop on later successful starts.
func collectConcurrentStartResults(t *testing.T, recorderService *recorder.Service, results <-chan concurrentStartResult) []int {
	t.Helper()

	started := make([]int, 0)
	var firstErr error
	for r := range results {
		if r.err == nil {
			started = append(started, r.room)
			t.Logf("concurrent start ok: room=%d", r.room)
			continue
		}
		if firstErr == nil {
			firstErr = r.err
		}
	}

	if firstErr != nil {
		for _, rid := range started {
			_ = recorderService.Stop(rid)
		}
		waitUntilNoActiveRecordings(t, recorderService, 30*time.Second)
		handleRecordingStartErr(t, firstErr)
	}

	return started
}
