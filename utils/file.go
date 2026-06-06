package utils

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
)

// Helper to remove invalid filename characters
func SanitizeFilename(name string) string {
	// Replace invalid characters with underscore
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		".", "_",
	)
	return replacer.Replace(name)
}

func GetPathFormat(path string) string {
	return filepath.Ext(path)[1:]
}

func ChangePathFormat(path string, newFormat string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return path + "." + newFormat
	}
	return path[0:len(path)-len(ext)] + "." + newFormat
}

func IsFileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

// GetDiskSpace returns disk usage information for the given path
func GetDiskSpace(outputDir string) (*disk.UsageStat, error) {
	// Get the absolute path of the output directory
	fullPath, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, err
	}

	// Get disk usage statistics for the path
	return disk.Usage(fullPath)
}
