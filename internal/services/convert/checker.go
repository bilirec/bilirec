package convert

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// checkOriginalFile uses ffprobe to check if the original video file contains video and audio streams.
// It returns (true, nil) if both video and audio streams are present.
// It returns (false, nil) if either is missing.
// It returns (true, error) if there was an error during the check (for example if ffprobe is unavailable),
// in which case conversion is allowed to proceed but the warning is logged.
func (c *Service) checkOriginalFile(path string) (bool, error) {

	if hasVideo, err := hasVideoStream(c.ctx, path); err != nil {
		return true, fmt.Errorf("检查原视频 %s 的视频流失败: %v", path, err)
	} else if !hasVideo {
		return false, fmt.Errorf("原视频 %s 不包含视频流，转换后将没有画面", path)
	}

	if hasAudio, err := hasAudioStream(c.ctx, path); err != nil {
		return true, fmt.Errorf("检查原视频 %s 的音频流失败: %v", path, err)
	} else if !hasAudio {
		return false, fmt.Errorf("原视频 %s 不包含音频流，转换后将没有声音", path)
	}

	return true, nil
}

func hasVideoStream(ctx context.Context, inputPath string) (bool, error) {
	if err := checkFFProbeAvailable(); err != nil {
		return true, err
	}

	cmd := exec.CommandContext(ctx,
		"ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=index",
		"-of", "csv=p=0",
		inputPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(string(output)) != "", nil
}

func hasAudioStream(ctx context.Context, inputPath string) (bool, error) {
	if err := checkFFProbeAvailable(); err != nil {
		return true, err
	}

	cmd := exec.CommandContext(ctx,
		"ffprobe",
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=index",
		"-of", "csv=p=0",
		inputPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(string(output)) != "", nil
}

func checkFFProbeAvailable() error {
	// Check for ffprobe
	_, err := exec.LookPath("ffprobe")
	if err != nil {
		return errors.New("没有检测到 ffprobe, 将直接跳过原始视频检查")
	}
	return nil
}
