package bilibili

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func TestAuth_LoadOfflineCredentials_ReadSuccess(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, "_cookies")
	tokenPath := filepath.Join(dir, "_refresh_token")

	wantCookie := "SESSDATA=abc; bili_jct=def"
	wantToken := "refresh-token-123"

	if err := os.WriteFile(cookiePath, []byte(wantCookie), 0600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte(wantToken), 0600); err != nil {
		t.Fatalf("write refresh token file: %v", err)
	}

	c := &Client{
		cookiePath:       cookiePath,
		refreshTokenPath: tokenPath,
	}

	gotCookie, gotToken, err := c.loadOfflineCredentials()
	if err != nil {
		t.Fatalf("loadOfflineCredentials should not fail: %v", err)
	}
	if gotCookie != wantCookie {
		t.Fatalf("cookie mismatch, want %q got %q", wantCookie, gotCookie)
	}
	if gotToken != wantToken {
		t.Fatalf("refresh token mismatch, want %q got %q", wantToken, gotToken)
	}
}

func TestAuth_LoadOfflineCredentials_MissingRefreshTokenReturnsError(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, "_cookies")
	if err := os.WriteFile(cookiePath, []byte("SESSDATA=abc"), 0600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	c := &Client{
		cookiePath:       cookiePath,
		refreshTokenPath: filepath.Join(dir, "_refresh_token"),
	}

	gotCookie, gotToken, err := c.loadOfflineCredentials()
	if err == nil {
		t.Fatalf("expected error when refresh token file is missing")
	}
	if gotCookie != "SESSDATA=abc" || gotToken != "" {
		t.Fatalf("unexpected credentials, got cookie=%q token=%q", gotCookie, gotToken)
	}
}

func TestAuth_InitQRLogin_NotControllerMode(t *testing.T) {
	c := &Client{loginMode: "startup"}

	qrcode, err := c.InitQRLogin()
	if !errors.Is(err, ErrNotControllerMode) {
		t.Fatalf("expected ErrNotControllerMode, got: %v", err)
	}
	if qrcode != nil {
		t.Fatalf("expected nil qrcode when not in controller mode")
	}
}

func TestAuth_InitQRLogin_ReusesExistingQRCode(t *testing.T) {
	tests := []struct {
		name  string
		state AuthState
	}{
		{name: "awaiting_qr", state: StateAwaitingQR},
		{name: "authenticating", state: StateAuthenticating},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{loginMode: "controller"}
			c.session.Store(&AuthSession{
				State:               tc.state,
				QrcodeURL:           "https://example.com/qr",
			})

			qrcode, err := c.InitQRLogin()
			if err != nil {
				t.Fatalf("InitQRLogin should reuse existing qrcode: %v", err)
			}
			if qrcode == nil || qrcode.Url != "https://example.com/qr" {
				t.Fatalf("expected reused qrcode url, got %#v", qrcode)
			}
		})
	}
}

func TestAuth_AutoRefreshIfSuccess_ReturnsInputError(t *testing.T) {
	c := &Client{}
	wantErr := errors.New("boom")

	gotErr := c.autoRefreshIfSuccess(context.Background(), wantErr)
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("expected original error, got: %v", gotErr)
	}
}

func TestAuth_AutoRefreshIfSuccess_WithCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := &Client{}
	if err := c.autoRefreshIfSuccess(ctx, nil); err != nil {
		t.Fatalf("autoRefreshIfSuccess should return nil when input err is nil: %v", err)
	}

	// Give goroutine a short window to observe canceled context and exit.
	time.Sleep(10 * time.Millisecond)
}

func TestAuth_SyncCookieToClient_UpdateAndAppend(t *testing.T) {
	client := resty.New()
	client.Cookies = []*http.Cookie{
		{Name: "SESSDATA", Value: "old"},
	}

	syncCookieToClient(client, []*http.Cookie{
		{Name: "SESSDATA", Value: "new"},
		{Name: "bili_jct", Value: "csrf"},
	})

	if len(client.Cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(client.Cookies))
	}
	if v := findCookieValue(client.Cookies, "SESSDATA"); v != "new" {
		t.Fatalf("expected updated SESSDATA cookie value 'new', got %q", v)
	}
	if v := findCookieValue(client.Cookies, "bili_jct"); v != "csrf" {
		t.Fatalf("expected appended bili_jct cookie value 'csrf', got %q", v)
	}
}

func findCookieValue(cookies []*http.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}
