package notify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/bilirec/bilirec/internal/modules/config"
	ns "github.com/bilirec/bilirec/internal/services/notify"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func setEnv(tb testing.TB, key, value string) {
	tb.Helper()
	prev, hadPrev := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		tb.Fatalf("failed to set env %s: %v", key, err)
	}
	tb.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func newNotifyService(tb testing.TB, webPushSubscriber string) *ns.Service {
	tb.Helper()

	secretDir := tb.TempDir()
	databaseDir := tb.TempDir()
	setEnv(tb, "SECRET_DIR", secretDir)
	setEnv(tb, "DATABASE_DIR", databaseDir)
	setEnv(tb, "WEBPUSH_SUBSCRIBER", webPushSubscriber)

	var svc *ns.Service
	app := fxtest.New(tb,
		config.Module,
		fx.Provide(ns.NewService),
		fx.Populate(&svc),
	)
	app.RequireStart()
	tb.Cleanup(func() {
		app.RequireStop()
	})

	return svc
}

func TestService_WebPushConfigAndSubscriptions(t *testing.T) {
	svc := newNotifyService(t, "mailto:test@example.com")

	state := svc.WebPushServiceState()

	if !state.Enabled {
		t.Fatal("expected web push to be enabled")
	}

	if state.PublicKey == "" {
		t.Fatal("expected non-empty web push public key")
	}

	err := svc.AddWebPushSubscription(webpush.Subscription{
		Endpoint: "https://push.example/send/abc",
		Keys: webpush.Keys{
			Auth:   "auth-token",
			P256dh: "p256dh-token",
		},
	})
	if err != nil {
		t.Fatalf("expected adding subscription to succeed but got error: %v", err)
	}

	err = svc.AddWebPushSubscription(webpush.Subscription{
		Endpoint: "https://push.example/send/def",
		Keys: webpush.Keys{
			Auth:   "auth-token-updated",
			P256dh: "p256dh-token-updated",
		},
	})
	if err != nil {
		t.Fatalf("expected adding subscription to succeed but got error: %v", err)
	}

	err = svc.AddWebPushSubscription(webpush.Subscription{
		Endpoint: "https://push.example/send/abc",
		Keys: webpush.Keys{
			Auth:   "auth-token-updated",
			P256dh: "p256dh-token-updated",
		},
	})
	if err != nil {
		t.Fatalf("expected adding existing subscription to succeed but got error: %v", err)
	}

	if err := svc.RemoveWebPushSubscription("https://push.example/send/abc"); err != nil {
		t.Fatalf("expected removing existing subscription to succeed but got error: %v", err)
	}

	if err := svc.RemoveWebPushSubscription("https://push.example/send/xyz"); err != nil {
		t.Fatalf("expected removing non-existing subscription to succeed but got error: %v", err)
	}
}

func TestService_PublishLiveStateFanoutToSSE(t *testing.T) {
	setEnv(t, "NOTIFY_SSE_TOKEN", "sse-token")
	svc := newNotifyService(t, "")

	clientID, ch, err := svc.SubscribeSSE("sse-token")
	if err != nil {
		t.Fatalf("expected subscribing sse to succeed but got error: %v", err)
	}
	defer svc.UnsubscribeSSE(clientID)

	svc.PublishLiveState(7788, "tester", "room title", ns.LiveStateLiveDetected)

	select {
	case payload := <-ch:
		var event ns.Event
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("expected valid event json but got error: %v", err)
		}
		if event.RoomID != 7788 {
			t.Fatalf("expected room id 7788 but got %d", event.RoomID)
		}
		if event.Type != string(ns.LiveStateLiveDetected) {
			t.Fatalf("expected type %s but got %s", ns.LiveStateLiveDetected, event.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected SSE payload but timed out")
	}
}

func TestService_SubscribeSSEValidation(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		svc := newNotifyService(t, "")
		_, _, err := svc.SubscribeSSE("token")
		if err != ns.ErrSSEDisabled {
			t.Fatalf("expected ErrSSEDisabled but got %v", err)
		}
	})

	t.Run("missing token", func(t *testing.T) {
		setEnv(t, "NOTIFY_SSE_TOKEN", "server-token")
		svc := newNotifyService(t, "")
		_, _, err := svc.SubscribeSSE("")
		if err != ns.ErrSSETokenMissing {
			t.Fatalf("expected ErrSSETokenMissing but got %v", err)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		setEnv(t, "NOTIFY_SSE_TOKEN", "server-token")
		svc := newNotifyService(t, "")
		_, _, err := svc.SubscribeSSE("bad-token")
		if err != ns.ErrSSETokenInvalid {
			t.Fatalf("expected ErrSSETokenInvalid but got %v", err)
		}
	})
}

func repoRootFromThisFile(tb testing.TB) string {
	tb.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("failed to resolve current file path")
	}

	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		tb.Fatalf("failed to resolve repo root from test file: %v", err)
	}

	return root
}
