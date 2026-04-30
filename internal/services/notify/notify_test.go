package notify_test

import (
	"os"
	"path/filepath"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/eric2788/bilirec/internal/modules/config"
	ns "github.com/eric2788/bilirec/internal/services/notify"
	push "github.com/eric2788/bilirec/pkg/webpush"
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

func newNotifyService(tb testing.TB, webPushSubscriber string) (*ns.Service, string) {
	tb.Helper()

	secretDir := tb.TempDir()
	setEnv(tb, "SECRET_DIR", secretDir)
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

	return svc, secretDir
}

func TestService_WebPushConfigAndSubscriptions(t *testing.T) {
	svc, secretDir := newNotifyService(t, "mailto:test@example.com")

	if !svc.WebPushEnabled() {
		t.Fatal("expected web push to be enabled")
	}

	if got := svc.WebPushPublicKey(); got == "" {
		t.Fatal("expected non-empty web push public key")
	}

	publicPath := filepath.Join(secretDir, push.PublicKeyFileName)
	privatePath := filepath.Join(secretDir, push.PrivateKeyFileName)
	if _, err := os.Stat(publicPath); err != nil {
		t.Fatalf("expected persisted public key file: %v", err)
	}
	if _, err := os.Stat(privatePath); err != nil {
		t.Fatalf("expected persisted private key file: %v", err)
	}

	created := svc.AddWebPushSubscription(webpush.Subscription{
		Endpoint: "https://push.example/send/abc",
		Keys: webpush.Keys{
			Auth:   "auth-token",
			P256dh: "p256dh-token",
		},
	})
	if !created {
		t.Fatal("expected adding subscription to succeed")
	}

	created = svc.AddWebPushSubscription(webpush.Subscription{
		Endpoint: "https://push.example/send/abc",
		Keys: webpush.Keys{
			Auth:   "auth-token-updated",
			P256dh: "p256dh-token-updated",
		},
	})
	if !created {
		t.Fatal("expected duplicate add to replace existing subscription")
	}

	if ok := svc.RemoveWebPushSubscription("https://push.example/send/abc"); !ok {
		t.Fatal("expected removing existing subscription to succeed")
	}

	if ok := svc.RemoveWebPushSubscription("https://push.example/send/abc"); ok {
		t.Fatal("expected removing non-existing subscription to fail")
	}

	if ok := svc.AddWebPushSubscription(webpush.Subscription{}); ok {
		t.Fatal("expected invalid subscription to be rejected")
	}
}
