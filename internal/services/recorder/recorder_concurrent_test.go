package recorder_test

import (
	"sync"
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
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// TestRecorder_MaxConcurrentRecordingsRace_Guard ensures concurrent starts never
// exceed MAX_CONCURRENT_RECORDINGS. Any overflow indicates a regression.
func TestRecorder_MaxConcurrentRecordingsRace_Guard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping race repro in short mode")
	}

	t.Setenv("MAX_CONCURRENT_RECORDINGS", "3")

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

	rooms := resolveLiveTestRoomIDs(t, roomService, 4)
	rounds := 20
	const (
		settleDelay    = 200 * time.Millisecond
		cleanupTimeout = 12 * time.Second
	)

	for round := 1; round <= rounds; round++ {
		roomService.InvalidateRooms(rooms...)

		startGate := make(chan struct{})
		resultCh := make(chan error, len(rooms))
		var wg sync.WaitGroup

		for _, roomID := range rooms {
			rid := roomID
			wg.Go(func() {
				<-startGate
				err := recorderService.Start(rid)
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
		active := recorderService.ListRecordingSize()

		if active == 4 {
			t.Fatalf("round %d overflow detected: success=%d active=%d max_limit_err=%d other_err=%d", round, success, active, maxLimitErr, otherErr)
		} else {
			t.Logf("round %d: success=%d active=%d max_limit_err=%d other_err=%d", round, success, active, maxLimitErr, otherErr)
		}

		for _, roomID := range rooms {
			recorderService.Stop(roomID)
		}
		waitUntilNoActiveRecordings(t, recorderService, cleanupTimeout)
	}
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
