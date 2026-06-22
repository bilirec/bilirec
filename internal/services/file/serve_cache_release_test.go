package file

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
)

func withServeCacheReadOnly(t *testing.T, drop bool, coldSecs int) {
	t.Helper()
	old := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyForTest(drop, coldSecs)
	t.Cleanup(func() { config.ReadOnly = old })
}

func newServiceForServeCacheTest(dropFn func(string) error, idleDelay time.Duration) *Service {
	s := &Service{
		serveCache: make(map[string]*serveCacheSession),
	}
	if dropFn != nil {
		s.dropPageCacheFn = dropFn
	}
	if idleDelay > 0 {
		s.serveCacheIdleDelayOverride = idleDelay
	}
	return s
}

func TestBeginServeCacheRelease_DropDisabled(t *testing.T) {
	withServeCacheReadOnly(t, false, 60)
	var drops atomic.Int32
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 10*time.Millisecond)

	done := s.BeginServeCacheRelease("/tmp/video.mp4")
	done()
	time.Sleep(20 * time.Millisecond)

	if drops.Load() != 0 {
		t.Fatalf("expected no drop when DROP_FILE_PAGE_CACHE=false, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_IdleDelay(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 30*time.Millisecond)

	path := "/tmp/video.mp4"
	done := s.BeginServeCacheRelease(path)
	done()

	time.Sleep(10 * time.Millisecond)
	if drops.Load() != 0 {
		t.Fatal("expected no drop before idle delay")
	}

	time.Sleep(30 * time.Millisecond)
	if drops.Load() != 1 {
		t.Fatalf("expected one drop after idle delay, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_ResetTimerOnReuse(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 50*time.Millisecond)

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done1()

	time.Sleep(30 * time.Millisecond)
	if drops.Load() != 0 {
		t.Fatal("expected no drop while idle timer was reset by second Begin")
	}

	done2 := s.BeginServeCacheRelease(path)
	done2()

	time.Sleep(30 * time.Millisecond)
	if drops.Load() != 0 {
		t.Fatal("expected timer reset: no drop until full idle after second serve")
	}

	time.Sleep(30 * time.Millisecond)
	if drops.Load() != 1 {
		t.Fatalf("expected one drop after second idle period, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_UsesDefaultWhenColdZero(t *testing.T) {
	withServeCacheReadOnly(t, true, 0)
	var drops atomic.Int32
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 30*time.Millisecond)

	path := "/tmp/video.mp4"
	done := s.BeginServeCacheRelease(path)
	done()

	time.Sleep(10 * time.Millisecond)
	if drops.Load() != 0 {
		t.Fatal("expected no drop before idle delay when cold=0 uses default")
	}

	time.Sleep(30 * time.Millisecond)
	if drops.Load() != 1 {
		t.Fatalf("expected one drop after default idle delay when cold=0, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_NoDropWhileActive(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 20*time.Millisecond)

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done2 := s.BeginServeCacheRelease(path)
	done1()

	time.Sleep(40 * time.Millisecond)
	if drops.Load() != 0 {
		t.Fatal("expected no drop while second serve active")
	}

	done2()
	time.Sleep(40 * time.Millisecond)
	if drops.Load() != 1 {
		t.Fatalf("expected drop after all serves end, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_ConcurrentBegin(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 30*time.Millisecond)

	path := "/tmp/video.mp4"
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := s.BeginServeCacheRelease(path)
			time.Sleep(5 * time.Millisecond)
			done()
		}()
	}
	wg.Wait()

	time.Sleep(50 * time.Millisecond)
	if drops.Load() != 1 {
		t.Fatalf("expected exactly one drop after concurrent serves, got %d", drops.Load())
	}
}

func TestStopServeCacheRelease_CancelsPendingTimer(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 50*time.Millisecond)

	done := s.BeginServeCacheRelease("/tmp/video.mp4")
	done()
	s.stopServeCacheRelease()

	time.Sleep(80 * time.Millisecond)
	if drops.Load() != 0 {
		t.Fatal("expected shutdown to cancel pending drop")
	}
}
