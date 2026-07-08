package updatecheck

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatCheckError_RateLimit(t *testing.T) {
	raw := `GET https://api.github.com/repos/bilirec/bilirec/tags: 403 API rate limit exceeded for 210.3.196.182. (But here's the good news: Authenticated requests get a higher rate limit. Check out the documentation for more details.) [rate reset in 28m35s]`

	formatted := formatCheckError(errors.New(raw))
	if formatted.Code != ErrorCodeGitHubRateLimit {
		t.Fatalf("expected code %q, got %q", ErrorCodeGitHubRateLimit, formatted.Code)
	}
	if formatted.RetryAfterSecs != 28*60+35 {
		t.Fatalf("expected retry_after 1715, got %d", formatted.RetryAfterSecs)
	}
	if !strings.Contains(formatted.Message, "29 分钟") {
		t.Fatalf("expected rounded minutes in message, got %q", formatted.Message)
	}
	if strings.Contains(formatted.Message, "api.github.com") {
		t.Fatalf("message should not expose raw API error, got %q", formatted.Message)
	}
}

func TestFormatCheckError_Unreachable(t *testing.T) {
	formatted := formatCheckError(errors.New(`Get "https://api.github.com/repos/bilirec/bilirec/tags": dial tcp: i/o timeout`))
	if formatted.Code != ErrorCodeGitHubUnreachable {
		t.Fatalf("expected code %q, got %q", ErrorCodeGitHubUnreachable, formatted.Code)
	}
}

func TestFormatCheckError_GenericGitHub(t *testing.T) {
	formatted := formatCheckError(errors.New(`GET https://api.github.com/repos/bilirec/bilirec/tags: 500 Internal Server Error`))
	if formatted.Code != ErrorCodeGitHubError {
		t.Fatalf("expected code %q, got %q", ErrorCodeGitHubError, formatted.Code)
	}
}

func TestParseRateResetSeconds(t *testing.T) {
	if got := parseRateResetSeconds("[rate reset in 1h2m3s]"); got != 3723 {
		t.Fatalf("expected 3723, got %d", got)
	}
}
