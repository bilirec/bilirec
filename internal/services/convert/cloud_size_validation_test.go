package convert_test

import (
	"testing"

	"github.com/eric2788/bilirec/internal/services/convert"
)

func TestIsConvertedFileInvalid(t *testing.T) {
	tests := []struct {
		name       string
		downloaded int64
		input      int64
		want       bool
	}{
		{
			name:       "invalid when output less than 1MB",
			downloaded: convert.MinimumExportedFileBytesRequired - 1,
			input:      10 * 1024 * 1024,
			want:       true,
		},
		{
			name:       "invalid when output less than half of input",
			downloaded: 3 * 1024 * 1024,
			input:      7 * 1024 * 1024,
			want:       true,
		},
		{
			name:       "valid at exact 1MB and half of input",
			downloaded: convert.MinimumExportedFileBytesRequired,
			input:      2 * convert.MinimumExportedFileBytesRequired,
			want:       false,
		},
		{
			name:       "valid when source size unavailable and output above 1MB",
			downloaded: 2 * convert.MinimumExportedFileBytesRequired,
			input:      0,
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := convert.IsConvertedFileInvalid(tc.downloaded, tc.input)
			if got != tc.want {
				t.Fatalf("isConvertedFileInvalid(%d, %d) = %v, want %v", tc.downloaded, tc.input, got, tc.want)
			}
		})
	}
}
