package config

import "testing"

func TestParseLocalLogsOverflow(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"", "drop"},
		{"drop", "drop"},
		{"DROP", "drop"},
		{"block", "block"},
		{" BLOCK ", "block"},
		{"unknown", "drop"},
	}
	for _, tt := range tests {
		if got := parseLocalLogsOverflow(tt.raw); got != tt.want {
			t.Errorf("parseLocalLogsOverflow(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
