package bilibili

import (
	bili "github.com/CuteReimu/bilibili/v2"
)

// AuthState represents the current bilibili authentication state
type AuthState string

const (
	StateIdle           AuthState = "idle"           // No authentication yet
	StatePreloaded      AuthState = "preloaded"      // Offline credentials loaded
	StateAwaitingQR     AuthState = "awaiting_qr"    // QR generated, waiting for scan
	StateAuthenticating AuthState = "authenticating" // QR scanned, authenticating
	StateAuthenticated  AuthState = "authenticated"  // Successfully authenticated
	StateFailed         AuthState = "failed"         // Authentication failed
	StateQRExpired      AuthState = "qr_expired"     // QR code expired, needs re-authentication
)

// AuthSession holds the current authentication session state
type AuthSession struct {
	State     AuthState
	QrcodeURL string
	Account   *bili.AccountInformation
	Error     error

	cookieRefreshCancel func() // Optional function to cancel ongoing cookie refresh, if applicable
}

// GetSession returns a snapshot of the current session
func (c *Client) GetSession() *AuthSession {
	current := c.session.Load()
	if current == nil {
		return &AuthSession{State: StateIdle}
	}
	snapshot := *current
	return &snapshot
}

func (c *Client) updateSession(fn func(*AuthSession)) {
	for {
		loaded := c.session.Load()
		base := loaded
		if base == nil {
			base = &AuthSession{State: StateIdle}
		}

		next := *base
		fn(&next)
		nextPtr := &next

		if loaded == nil {
			if c.session.CompareAndSwap(nil, nextPtr) {
				return
			}
			continue
		}

		if c.session.CompareAndSwap(loaded, nextPtr) {
			return
		}
	}
}

func (s *AuthSession) cancelAutoRefreshCookies() {
	if s.cookieRefreshCancel != nil {
		s.cookieRefreshCancel()
	}
}
