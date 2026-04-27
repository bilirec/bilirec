package recorder_test

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/internal/services/convert"
	"github.com/eric2788/bilirec/internal/services/path"
	"github.com/eric2788/bilirec/internal/services/recorder"
	"github.com/eric2788/bilirec/internal/services/stream"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestFlvRecord(t *testing.T) {

	const room = 1777483153

	var recorderService *recorder.Service

	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Provide(path.NewService),
		fx.Provide(stream.NewService),
		fx.Provide(convert.NewService),
		fx.Provide(recorder.NewService),
		fx.Populate(&recorderService),
	)

	app.RequireStart()
	defer app.RequireStop()

	var m1, m2, m3 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	t.Log("start it manually")
	err := recorderService.Start(room)
	if err != nil {
		if err == recorder.ErrStreamNotLive {
			t.Skip(err)
		}
		t.Fatal(err)
	}

	<-time.After(15 * time.Second)

	runtime.ReadMemStats(&m2)

	t.Log("stop it manually")
	t.Logf("stop success: %v", recorderService.Stop(room))

	<-time.After(5 * time.Second)
	runtime.ReadMemStats(&m3)

	t.Logf("memory before start: %.2f MB", float64(m1.Alloc/1024/1024))
	t.Logf("memory before stop: %.2f MB", float64(m2.Alloc/1024/1024))
	t.Logf("memory after stop: %.2f MB", float64(m3.Alloc/1024/1024))
	t.Logf("growth during recording: %.2f MB", float64((m2.Alloc-m1.Alloc)/1024/1024))
	t.Logf("growth after stop: %.2f MB", float64((m3.Alloc-m2.Alloc)/1024/1024))
}

func TestChannelRangeReturnedWhileStreaming(t *testing.T) {
	ch := make(chan int, 10)
	// give some random elements keep sending to channel
	send := func() {
		for i := 0; i < 10; i++ {
			ch <- i
			time.Sleep(1 * time.Second)
		}
		close(ch)
	}
	go send()
	// range over channel and print elements
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

func init() {
	if os.Getenv("CI") != "" {
		os.Setenv("ANONYMOUS_LOGIN", "true")
	}
}
