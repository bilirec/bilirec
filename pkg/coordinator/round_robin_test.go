package coordinator_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/coordinator"
)

func TestRoundRobin_OneSignalPerParticipant(t *testing.T) {
	const (
		n           = 3
		cyclePeriod = 300 * time.Millisecond
	)
	rr := coordinator.NewRoundRobin(cyclePeriod)
	rr.SetMinTick(20 * time.Millisecond)

	chans := make([]<-chan struct{}, n)
	unregs := make([]func(), n)
	for i := 0; i < n; i++ {
		ch, unreg := rr.Register(nil)
		chans[i] = ch
		unregs[i] = unreg
	}
	defer func() {
		for _, u := range unregs {
			u()
		}
	}()

	deadline := time.After(cyclePeriod + 250*time.Millisecond)
	got := make([]int, n)
	for sum := 0; sum < n; {
		select {
		case <-chans[0]:
			got[0]++
			sum++
		case <-chans[1]:
			got[1]++
			sum++
		case <-chans[2]:
			got[2]++
			sum++
		case <-deadline:
			t.Fatalf("timeout, got=%v", got)
		}
	}
	for i, c := range got {
		if c != 1 {
			t.Fatalf("participant %d signals=%d want 1", i, c)
		}
	}
}

func TestRoundRobin_SkipsNotReady(t *testing.T) {
	rr := coordinator.NewRoundRobin(100 * time.Millisecond)
	rr.SetMinTick(10 * time.Millisecond)

	ch, unreg := rr.Register(func() bool { return false })
	defer unreg()

	select {
	case <-ch:
		t.Fatal("should not signal when not ready")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRoundRobin_ReadyBecomesTrueLater(t *testing.T) {
	rr := coordinator.NewRoundRobin(150 * time.Millisecond)
	rr.SetMinTick(10 * time.Millisecond)

	var flag atomic.Bool
	ch, unreg := rr.Register(func() bool { return flag.Load() })
	defer unreg()

	flag.Store(true)

	select {
	case <-ch:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected signal after ready becomes true")
	}
}

func TestRoundRobin_UnregisterShrinksParticipants(t *testing.T) {
	rr := coordinator.NewRoundRobin(100 * time.Millisecond)
	rr.SetMinTick(10 * time.Millisecond)

	_, unreg1 := rr.Register(nil)
	ch2, unreg2 := rr.Register(nil)

	unreg1()
	defer unreg2()

	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-ch2:
			return
		case <-deadline:
			t.Fatal("expected remaining participant to keep receiving signals after unregister")
		}
	}
}
