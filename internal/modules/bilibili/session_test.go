package bilibili

import (
	"sync"
	"testing"
	"time"
)

func TestSession_UpdateSession_FromNilInitialState(t *testing.T) {
	c := &Client{}

	done := make(chan struct{})
	go func() {
		c.updateSession(func(s *AuthSession) {
			s.State = StateAwaitingQR
			s.QrcodeURL = "https://example.com/qr"
		})
		close(done)
	}()

	select {
	case <-done:
		// continue
	case <-time.After(500 * time.Millisecond):
		t.Fatal("updateSession should not block when session is initially nil")
	}

	got := c.GetSession()
	if got.State != StateAwaitingQR {
		t.Fatalf("unexpected state, want %q got %q", StateAwaitingQR, got.State)
	}
	if got.QrcodeURL != "https://example.com/qr" {
		t.Fatalf("unexpected qrcode url, want %q got %q", "https://example.com/qr", got.QrcodeURL)
	}
}

func TestSession_UpdateSession_ConcurrentFromNil(t *testing.T) {
	c := &Client{}

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			c.updateSession(func(s *AuthSession) {
				s.State = StateAuthenticated
			})
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// continue
	case <-time.After(1 * time.Second):
		t.Fatal("concurrent updateSession should complete without deadlock")
	}

	if got := c.GetSession(); got.State != StateAuthenticated {
		t.Fatalf("unexpected state after concurrent updates, want %q got %q", StateAuthenticated, got.State)
	}
}
