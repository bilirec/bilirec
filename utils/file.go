package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bilirec/bilirec/pkg/ds"
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

// --- Recording output layout (rotateFilePath / OUTPUT_DIR/{uname}-{roomID}/...) ---

// IsRecordingMediaFilename reports whether name is a recording media file (not a directory).
func IsRecordingMediaFilename(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return recordingMediaExtensions.Contains(ext)
}

// ParseRecordingSegmentFilename extracts the trailing timestamp and optional rotation segment from a media basename.
func ParseRecordingSegmentFilename(name string) (stamp string, segment int, ok bool) {
	if !IsRecordingMediaFilename(name) {
		return "", 0, false
	}
	m := recordingNameStamp.FindStringSubmatch(strings.TrimSuffix(name, filepath.Ext(name)))
	if m == nil {
		return "", 0, false
	}
	segment = 0
	if m[2] != "" {
		segment, _ = strconv.Atoi(m[2])
	}
	return m[1], segment, true
}

// RoomRecordingDirs lists output subdirectories whose name ends with "-{roomID}".
func RoomRecordingDirs(outputDir string, roomID int) []string {
	want := strconv.Itoa(roomID)
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if i := strings.LastIndex(name, "-"); i >= 0 && name[i+1:] == want {
			dirs = append(dirs, filepath.Join(outputDir, name))
		}
	}
	return dirs
}

var recordingMediaExtensions = ds.SetFrom(
	".mp4", ".m4a", ".ts", ".fmp4", ".flv",
)

// recordingNameStamp matches the filename timestamp suffix produced by rotateFilePath.
var recordingNameStamp = regexp.MustCompile(`-(\d{8}_\d{6})(?:-(\d+))?$`)
