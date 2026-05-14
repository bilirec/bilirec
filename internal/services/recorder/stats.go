package recorder

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Stats struct {
	BytesWritten   uint64       `json:"bytes_written"`
	Status         RecordStatus `json:"status"`
	StartTime      int64        `json:"start_time"`
	ElapsedSeconds int64        `json:"elapsed_seconds"`
	OutputPath     string       `json:"output_path"`
}

func (r *Service) GetStatus(roomId int) RecordStatus {
	info, ok := r.recording.Load(roomId)
	if !ok {
		return Idle
	} else if status := info.status.Load(); status == nil {
		return Idle
	} else {
		return *status
	}
}

func (r *Service) ListRecording() []int {
	rooms := make([]int, 0)
	r.recording.Range(func(key int, value *Info) bool {
		rooms = append(rooms, key)
		return true
	})
	return rooms
}

func (r *Service) GetStats(roomId int) (*Stats, bool) {
	info, ok := r.recording.Load(roomId)
	if !ok {
		return nil, false
	}
	status := r.GetStatus(roomId)
	return &Stats{
		BytesWritten:   info.bytesRead.Load(),
		Status:         status,
		StartTime:      info.startTime.Unix(),
		ElapsedSeconds: int64(time.Since(info.startTime).Seconds()),
		OutputPath:     info.OutputPath(),
	}, true
}

func (r *Service) IsRecording(path string) bool {
	return r.writingFiles.Contains(filepath.Base(path))
}

func (r *Service) GetStatuses(roomIds []int) map[string]RecordStatus {
	result := make(map[string]RecordStatus, len(roomIds))
	for _, roomId := range roomIds {
		status := r.GetStatus(roomId)
		result[strconv.Itoa(roomId)] = status
	}
	return result
}

func (r *Service) GetBatchStats(roomIds []int) map[string]*Stats {
	result := make(map[string]*Stats, len(roomIds))
	for _, roomId := range roomIds {
		if stats, ok := r.GetStats(roomId); ok {
			result[strconv.Itoa(roomId)] = stats
		}
	}
	return result
}

// IsRecordingUnder checks if any recordings are happening under the given relative path.
// The path format is {username}-{roomID} or {username}-{roomID}/{subpath}
func (r *Service) IsRecordingUnder(relPath string) bool {
	// Normalize the path
	cleanPath := filepath.Clean(relPath)
	parts := strings.SplitN(cleanPath, string(os.PathSeparator), 2)

	if len(parts) == 0 {
		return false
	}

	// Extract room ID from first segment: {username}-{roomID}
	dirName := parts[0]
	segments := strings.Split(dirName, "-")
	if len(segments) < 2 {
		return false
	}

	// Room ID should be the last segment after the last dash
	roomIdStr := segments[len(segments)-1]
	roomId, err := strconv.Atoi(roomIdStr)
	if err != nil {
		return false
	}

	_, ok := r.recording.Load(roomId)
	return ok
}
