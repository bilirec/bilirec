package danmaku

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathForVideo maps a video segment path to its paired danmaku sidecar path
// with the given extension (e.g. ".jsonl" or ".xml").
func PathForVideo(videoPath, ext string) string {
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ext
}

// SidecarCandidates returns sidecar paths to try, preferring .jsonl over .xml
// so newer recordings win when both exist.
func SidecarCandidates(videoPath string) []string {
	return []string{
		PathForVideo(videoPath, ".jsonl"),
		PathForVideo(videoPath, ".xml"),
	}
}

// ResolveSidecar finds an existing danmaku sidecar for a video path by
// checking SidecarCandidates with os.Stat. videoPath may be absolute (tests)
// or already rooted under the output directory.
func ResolveSidecar(videoPath string) (string, error) {
	for _, p := range SidecarCandidates(videoPath) {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("danmaku sidecar not found for %s", videoPath)
}
