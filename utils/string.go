package utils

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func PrettyPrintJSON(v interface{}) string {
	bytes, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(bytes)
}

func TruncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}

func AsciiFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '"' || r == '\\' || r < 0x20 {
			continue
		}
		if r > 127 {
			b.WriteByte('?')
		} else {
			b.WriteRune(r)
		}
	}
	s := strings.ReplaceAll(b.String(), "/", "-")
	if s == "" {
		s = "file"
	}
	return s
}

func SplitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	var result []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// IsValidAbsoluteHTTPURL checks whether raw is an absolute HTTP/HTTPS URL with host.
func IsValidAbsoluteHTTPURL(raw string) bool {
	parsedURL, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsedURL.Host == "" {
		return false
	}
	return parsedURL.Scheme == "http" || parsedURL.Scheme == "https"
}
