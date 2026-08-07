package subcheck

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const (
	subcheckAutoRecordRoomsEnv    = "SUBCHECK_AUTO_RECORD_ROOMS"
	subcheckAutoRecordWaitSecsEnv = "SUBCHECK_AUTO_RECORD_WAIT_SECS"

	subcheckIntegrationTickSecs        = 1
	subcheckIntegrationMinIntervalSecs = 1
	subcheckIntegrationMaxIntervalSecs = 5
	subcheckIntegrationPollInterval    = 500 * time.Millisecond
	subcheckIntegrationCleanupTimeout  = 30 * time.Second
	subcheckIntegrationSettleAfterStop = 2 * time.Second
)

type subcheckIntegrationSession struct {
	app      *fxtest.App
	roomSvc  *room.Service
	subSvc   *subscribe.Service
	recSvc   *recorder.Service
	subcheck *Service
}

// TestSubcheck_AutoRecord_StartsRecording_Live verifies the subcheck loop discovers
// live subscribed rooms and triggers auto-recording within a bounded wait window.
func TestSubcheck_AutoRecord_StartsRecording_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live subcheck auto-record integration test in short mode")
	}

	sess := newSubcheckIntegrationSession(t)

	targetRooms := parseAutoRecordRoomCount(t)
	liveRooms := validateLiveRoomIDs(t, sess.roomSvc, targetRooms)
	t.Logf("validated %d live room(s): %v", len(liveRooms), liveRooms)

	var subscribedRooms []int
	t.Cleanup(func() {
		cleanupSubcheckIntegrationTest(t, sess, subscribedRooms)
	})

	subscribedRooms = subscribeRoomsForAutoRecord(t, sess.subSvc, liveRooms)

	// Startup already ran one full check before subscribe; trigger the next cycle immediately.
	sess.subcheck.tryStartAllAutoRecordRooms()

	waitTimeout := subcheckAutoRecordWaitTimeout(targetRooms)
	t.Logf("waiting up to %s for subcheck auto-record (tick=%ds min_interval=%ds)",
		waitTimeout, subcheckIntegrationTickSecs, subcheckIntegrationMinIntervalSecs)

	started := waitForAutoRecordStart(t, sess.recSvc, subscribedRooms, waitTimeout)
	if len(started) == 0 {
		t.Fatalf("subcheck did not start recording for any room within %s", waitTimeout)
	}
	t.Logf("auto-record started for room(s): %v", started)
}

func newSubcheckIntegrationSession(t *testing.T) *subcheckIntegrationSession {
	t.Helper()

	dataDir := t.TempDir()
	t.Setenv("DATABASE_DIR", dataDir)
	t.Setenv("OUTPUT_DIR", t.TempDir())
	t.Setenv("SUBCHECK_TICK_SECS", strconv.Itoa(subcheckIntegrationTickSecs))
	t.Setenv("SUBCHECK_MIN_INTERVAL_SECS", strconv.Itoa(subcheckIntegrationMinIntervalSecs))
	t.Setenv("SUBCHECK_MAX_INTERVAL_SECS", strconv.Itoa(subcheckIntegrationMaxIntervalSecs))
	t.Setenv("SUBCHECK_MAX_SHARDS", "1")
	if os.Getenv("CI") != "" {
		t.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
	}

	var roomSvc *room.Service
	var subSvc *subscribe.Service
	var recSvc *recorder.Service
	var subcheckSvc *Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		metrics.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(room.NewService),
		fx.Provide(subscribe.NewService),
		fx.Provide(notify.NewService),
		fx.Provide(recorder.NewService),
		fx.Provide(NewService),
		fx.Populate(&roomSvc, &subSvc, &recSvc, &subcheckSvc),
		fx.StartTimeout(60*time.Second),
	)
	app.RequireStart()
	t.Cleanup(func() {
		app.RequireStop()
	})

	return &subcheckIntegrationSession{
		app:      app,
		roomSvc:  roomSvc,
		subSvc:   subSvc,
		recSvc:   recSvc,
		subcheck: subcheckSvc,
	}
}

func parseAutoRecordRoomCount(tb testing.TB) int {
	tb.Helper()
	raw := strings.TrimSpace(os.Getenv(subcheckAutoRecordRoomsEnv))
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		tb.Fatalf("invalid %s value %q", subcheckAutoRecordRoomsEnv, raw)
	}
	return n
}

func subscribeRoomsForAutoRecord(t *testing.T, subSvc *subscribe.Service, roomIDs []int) []int {
	t.Helper()

	subscribed := make([]int, 0, len(roomIDs))
	for _, roomID := range roomIDs {
		if err := subSvc.Subscribe(roomID); err != nil && err != subscribe.ErrRoomAlreadySubscribed {
			t.Fatalf("subscribe room %d failed: %v", roomID, err)
		}
		if err := subSvc.UpdateConfig(roomID, &subscribe.RoomConfig{
			Notify:                false,
			AutoRecord:            true,
			RecordDurationMinutes: 1,
		}); err != nil {
			t.Fatalf("enable auto-record for room %d failed: %v", roomID, err)
		}
		subscribed = append(subscribed, roomID)
		t.Logf("subscribed room %d with auto_record=true", roomID)
	}
	return subscribed
}

func subcheckAutoRecordWaitTimeout(roomCount int) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(subcheckAutoRecordWaitSecsEnv)); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}

	sched := computeSchedule(roomCount, scheduleParamsFromConfig(
		50,
		subcheckIntegrationTickSecs,
		subcheckIntegrationMinIntervalSecs,
		subcheckIntegrationMaxIntervalSecs,
		32,
	))
	// Several shard ticks plus recorder startup latency.
	return sched.interval*5 + 45*time.Second
}

func waitForAutoRecordStart(t *testing.T, recSvc *recorder.Service, roomIDs []int, timeout time.Duration) []int {
	t.Helper()

	deadline := time.Now().Add(timeout)
	started := make([]int, 0, len(roomIDs))
	startedSet := make(map[int]struct{}, len(roomIDs))

	for time.Now().Before(deadline) {
		for _, roomID := range roomIDs {
			if _, ok := startedSet[roomID]; ok {
				continue
			}
			if recordingActive(recSvc.GetStatus(roomID)) {
				startedSet[roomID] = struct{}{}
				started = append(started, roomID)
				t.Logf("room %d recording active (status=%s)", roomID, recSvc.GetStatus(roomID))
			}
		}
		if len(started) > 0 {
			return started
		}

		statuses := make([]string, 0, len(roomIDs))
		for _, roomID := range roomIDs {
			statuses = append(statuses, fmt.Sprintf("%d=%s", roomID, recSvc.GetStatus(roomID)))
		}
		t.Logf("polling recorder status: %s", strings.Join(statuses, ", "))
		time.Sleep(subcheckIntegrationPollInterval)
	}

	return started
}

func recordingActive(status recorder.RecordStatus) bool {
	return status == recorder.Recording || status == recorder.Recovering
}

func cleanupSubcheckIntegrationTest(t *testing.T, sess *subcheckIntegrationSession, roomIDs []int) {
	t.Helper()
	if sess == nil || len(roomIDs) == 0 {
		return
	}

	for _, roomID := range roomIDs {
		if sess.recSvc.Stop(roomID) {
			t.Logf("stopped recording for room %d", roomID)
		}
	}
	waitUntilNoActiveRecordings(t, sess.recSvc, subcheckIntegrationCleanupTimeout)
	time.Sleep(subcheckIntegrationSettleAfterStop)

	for _, roomID := range roomIDs {
		if err := sess.subSvc.Unsubscribe(roomID); err != nil {
			t.Logf("unsubscribe room %d failed: %v", roomID, err)
			continue
		}
		t.Logf("unsubscribed room %d", roomID)
	}
}

func waitUntilNoActiveRecordings(t *testing.T, recSvc *recorder.Service, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if recSvc.ListRecordingSize() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("cleanup timeout: still has %d active recording(s)", recSvc.ListRecordingSize())
}
