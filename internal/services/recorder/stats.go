package recorder

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Stats struct {
	BytesWritten        uint64       `json:"bytes_written"`
	DanmakuBytesWritten uint64       `json:"danmaku_bytes_written"`
	RecordDanmaku       bool         `json:"record_danmaku"`
	Status              RecordStatus `json:"status"`
	StartTime           int64        `json:"start_time"`
	ElapsedSeconds      int64        `json:"elapsed_seconds"`
	OutputPath          string       `json:"output_path"`
	ActualQn            int          `json:"actual_qn"`
	ActualStreamFormat  string       `json:"actual_stream_format,omitempty"`
	IsAudioOnly         bool         `json:"is_audio_only"`
	RoomTitle           string       `json:"room_title"`
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

// ListRecordingSize returns the number of bytes written for the recording of the given room ID.
// it avoids slice copy by directly accessing the atomic uint64 in the Info struct.
func (r *Service) ListRecordingSize() int {
	return r.recording.Size()
}

func (r *Service) GetStats(roomId int) (*Stats, bool) {
	info, ok := r.recording.Load(roomId)
	if !ok {
		return nil, false
	}
	status := r.GetStatus(roomId)
	roomTitle := ""
	if info.room != nil {
		roomTitle = info.room.Title
	}
	return &Stats{
		BytesWritten:        info.bytesRead.Load(),
		DanmakuBytesWritten: r.dm.GetBytesWritten(roomId),
		RecordDanmaku:       info.startOptions.recordDanmaku,
		Status:              status,
		StartTime:           info.startTime.Unix(),
		ElapsedSeconds:      int64(time.Since(info.startTime).Seconds()),
		OutputPath:          info.OutputPath(),
		ActualQn:            int(info.actualQn.Load()),
		ActualStreamFormat:  info.actualStreamFormat.LoadOr(""),
		IsAudioOnly:         info.isAudioOnly.Load(),
		RoomTitle:           roomTitle,
	}, true
}

func (r *Service) IsRecording(path string) bool {
	base := filepath.Base(path)
	if r.writingFiles.Contains(base) {
		return true
	}
	if !isDanmakuSidecarExt(filepath.Ext(base)) {
		return false
	}
	queryStem := recordingFileStem(base)
	if queryStem == "" {
		return false
	}
	for _, active := range r.writingFiles.ToSlice() {
		if recordingFileStem(active) == queryStem {
			return true
		}
	}
	return false
}

func isDanmakuSidecarExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jsonl", ".xml":
		return true
	default:
		return false
	}
}

func recordingFileStem(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return ""
	}
	return strings.TrimSuffix(name, ext)
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
