package file

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrUnsupportedPlaybackMedia = errors.New("不支持的播放媒体格式")

// OpenForPlayback validates a relative path and returns an absolute path plus MIME type.
func (s *Service) OpenForPlayback(relPath string) (string, string, error) {
	fullPath, err := s.path.ValidatePath(relPath)
	if err != nil {
		return "", "", err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return "", "", ErrIsDirectory
	}

	mimeType, err := inferPlaybackMIME(fullPath)
	if err != nil {
		return "", "", err
	}

	return fullPath, mimeType, nil
}

func inferPlaybackMIME(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4":
		return "video/mp4", nil
	default:
		return "", ErrUnsupportedPlaybackMedia
	}
}
