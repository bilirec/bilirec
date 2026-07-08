package updatecheck

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const (
	ErrorCodeNone              = ""
	ErrorCodeNoEmbeddedVersion = "no_embedded_version"
	ErrorCodeGitHubRateLimit   = "github_rate_limit"
	ErrorCodeGitHubUnreachable = "github_unreachable"
	ErrorCodeGitHubError       = "github_error"
)

var rateResetPattern = regexp.MustCompile(`(?i)rate reset in (?:(\d+)h)?(?:(\d+)m)?(\d+)s`)

type formattedCheckError struct {
	Message        string
	Code           string
	RetryAfterSecs int
}

func formatCheckError(err error) formattedCheckError {
	if err == nil {
		return formattedCheckError{}
	}

	raw := err.Error()
	lower := strings.ToLower(raw)

	if isRateLimitError(lower) {
		retryAfter := parseRateResetSeconds(raw)
		return formattedCheckError{
			Code:           ErrorCodeGitHubRateLimit,
			RetryAfterSecs: retryAfter,
			Message:        rateLimitMessage(retryAfter),
		}
	}

	if isUnreachableError(err, lower) {
		return formattedCheckError{
			Code:    ErrorCodeGitHubUnreachable,
			Message: "暂时无法连接 GitHub，请检查网络后重试，或直接打开 Release 页面查看",
		}
	}

	return formattedCheckError{
		Code:    ErrorCodeGitHubError,
		Message: "检查更新失败，请稍后重试或直接打开 Release 页面查看",
	}
}

func isRateLimitError(lower string) bool {
	return strings.Contains(lower, "rate limit") ||
		(strings.Contains(lower, "403") && strings.Contains(lower, "api.github.com"))
}

func isUnreachableError(err error, lower string) bool {
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	unreachableHints := []string{
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
		"timeout",
		"tls handshake timeout",
		"network is unreachable",
		"dial tcp",
		"eof",
	}
	for _, hint := range unreachableHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func parseRateResetSeconds(raw string) int {
	match := rateResetPattern.FindStringSubmatch(raw)
	if match == nil {
		return 0
	}

	hours, _ := strconv.Atoi(strings.TrimSpace(match[1]))
	minutes, _ := strconv.Atoi(strings.TrimSpace(match[2]))
	seconds, _ := strconv.Atoi(strings.TrimSpace(match[3]))
	return hours*3600 + minutes*60 + seconds
}

func rateLimitMessage(retryAfterSecs int) string {
	if retryAfterSecs <= 0 {
		return "GitHub 检查请求过于频繁，请稍后重试，或直接打开 Release 页面查看"
	}

	minutes := (retryAfterSecs + 59) / 60
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf(
		"GitHub 检查请求过于频繁，请约 %d 分钟后重试，或直接打开 Release 页面查看",
		minutes,
	)
}

func failureResult(current string, err error) Result {
	formatted := formatCheckError(err)
	return Result{
		Current:        current,
		URL:            releasesURL,
		Error:          formatted.Message,
		ErrorCode:      formatted.Code,
		RetryAfterSecs: formatted.RetryAfterSecs,
	}
}

// LogFailure logs a failed check: friendly message at warn, raw error at debug.
func LogFailure(res Result, rawErr error) {
	if res.Error == "" || rawErr == nil {
		return
	}

	logger.Warn(res.Error)
	logger.Debugf("check update failed: %v", rawErr)
}
