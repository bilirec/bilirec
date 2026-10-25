package danmaku

import (
	"fmt"
	"os"

	"github.com/bilirec/bilirec/utils"
)

// SidecarCandidates returns danmaku sidecar paths to try, preferring .jsonl over .xml
// so newer recordings win when both exist.
func SidecarCandidates(videoPath string) []string {
	return []string{
		utils.SidecarPathForVideo(videoPath, ".jsonl"),
		utils.SidecarPathForVideo(videoPath, ".xml"),
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
