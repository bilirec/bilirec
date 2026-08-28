package recorder

import (
	"bytes"
	"testing"
)

type closeTracker struct {
	*bytes.Reader
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return nil
}

func TestDrainAndCloseBody(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 2048)
	tr := &closeTracker{Reader: bytes.NewReader(payload)}
	drainAndCloseBody(tr)
	if !tr.closed {
		t.Fatal("expected body to be closed")
	}
	if tr.Len() != 0 {
		t.Fatalf("expected body to be drained, remaining %d", tr.Len())
	}

	drainAndCloseBody(nil)
}
