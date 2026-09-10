package utils

import (
	"fmt"
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

// StagingPath is the sibling temporary path used while writing an output file.
func StagingPath(output string) string {
	return output + ".tmp"
}

// ReplaceFile moves tmp onto final. On Windows Rename cannot overwrite, so
// final is removed first when it already exists.
func ReplaceFile(tmp, final string) error {
	if err := os.Remove(final); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing file %s: %w", final, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, final, err)
	}
	return nil
}

// RemoveIfExists deletes path. Missing files are not an error.
func RemoveIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
