package webpush

import (
	"net/http"
	"testing"

	swebpush "github.com/SherClockHolmes/webpush-go"
)

func TestManager_AddSubscription_ReplacesExistingEndpoint(t *testing.T) {
	m := NewManager("mailto:test@example.com", t.TempDir(), &http.Client{})

	first := swebpush.Subscription{
		Endpoint: "https://push.example/send/abc",
		Keys: swebpush.Keys{
			Auth:   "auth-1",
			P256dh: "p256dh-1",
		},
	}
	if ok := m.AddSubscription(first); !ok {
		t.Fatal("expected first subscription add to succeed")
	}

	second := swebpush.Subscription{
		Endpoint: "https://push.example/send/abc",
		Keys: swebpush.Keys{
			Auth:   "auth-2",
			P256dh: "p256dh-2",
		},
	}
	if ok := m.AddSubscription(second); !ok {
		t.Fatal("expected duplicate endpoint add to replace existing subscription")
	}

	got, ok := m.subs.Load(second.Endpoint)
	if !ok {
		t.Fatal("expected endpoint to exist after replacement")
	}
	if got.Keys.Auth != second.Keys.Auth || got.Keys.P256dh != second.Keys.P256dh {
		t.Fatalf("expected replaced keys %+v, got %+v", second.Keys, got.Keys)
	}
}

func TestManager_AddSubscription_RejectsInvalidPayload(t *testing.T) {
	m := NewManager("mailto:test@example.com", t.TempDir(), &http.Client{})

	if ok := m.AddSubscription(swebpush.Subscription{}); ok {
		t.Fatal("expected invalid empty subscription to be rejected")
	}
}
