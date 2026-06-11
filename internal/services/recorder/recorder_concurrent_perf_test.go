package recorder_test

import (
	"sort"
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

func TestRecorder_ConcurrentStartBurst_Latency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping burst latency test in short mode")
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
	startGate := make(chan struct{})
	latencies := make([]time.Duration, 0, len(rooms))
	var latMu sync.Mutex
	var wg sync.WaitGroup
	for _, roomID := range rooms {
		rid := roomID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			begin := time.Now()
			_ = recorderService.Start(rid)
			lat := time.Since(begin)
			latMu.Lock()
			latencies = append(latencies, lat)
			latMu.Unlock()
		}()
	}
	close(startGate)
	wg.Wait()

	if len(latencies) == 0 {
		t.Fatal("no latency samples collected")
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[(len(latencies)-1)*95/100]
	t.Logf("concurrent start burst latency samples=%d p95=%s max=%s", len(latencies), p95, latencies[len(latencies)-1])

	for _, roomID := range rooms {
		recorderService.Stop(roomID)
	}
	waitUntilNoActiveRecordings(t, recorderService, 12*time.Second)
}
