package recorder

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Stats struct {
	BytesRead           uint64       `json:"bytes_read"`
	BytesWritten        uint64       `json:"bytes_written"`
	DanmakuBytesWritten uint64       `json:"danmaku_bytes_written"`
	RecordDanmaku       bool         `json:"record_danmaku"`
	RecordSubtitle      bool         `json:"record_subtitle"`
	Status              RecordStatus `json:"status"`
	StartTime           int64        `json:"start_time"`
	ElapsedSeconds      int64        `json:"elapsed_seconds"`
	OutputPath          string       `json:"output_path"`
	ActualQn            int          `json:"actual_qn"`
	ActualStreamFormat  string       `json:"actual_stream_format,omitempty"`
	IsAudioOnly         bool         `json:"is_audio_only"`
	RoomTitle           string       `json:"room_title"`

	FlushCount    uint64 `json:"flush_count,omitempty"`
	FlushMs       uint64 `json:"flush_ms,omitempty"`
	SyncCount     uint64 `json:"sync_count,omitempty"`
	SyncMs        uint64 `json:"sync_ms,omitempty"`
	EnqueueWaits  uint64 `json:"enqueue_waits,omitempty"`
	EnqueueWaitMs uint64 `json:"enqueue_wait_ms,omitempty"`
	TimelineGaps  uint64 `json:"timeline_gaps,omitempty"`
	TimelineGapMs uint64 `json:"timeline_gap_ms,omitempty"`
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
	var danmakuBytes uint64
	if r.dm != nil {
		danmakuBytes = r.dm.GetBytesWritten(roomId)
	}
	stats := info.stats

	return &Stats{
		BytesRead:           stats.BytesRead.Load(),
		BytesWritten:        stats.BytesWritten.Load(),
		DanmakuBytesWritten: danmakuBytes,
		RecordDanmaku:       info.startOptions.recordDanmaku,
		RecordSubtitle:      info.startOptions.recordSubtitle,
		Status:              status,
		StartTime:           info.startTime.Unix(),
		ElapsedSeconds:      int64(time.Since(info.startTime).Seconds()),
		OutputPath:          info.OutputPath(),
		ActualQn:            int(info.actualQn.Load()),
		ActualStreamFormat:  info.actualStreamFormat.LoadOr(""),
		IsAudioOnly:         info.isAudioOnly.Load(),
		RoomTitle:           roomTitle,
		FlushCount:          stats.FlushCount.Load(),
		FlushMs:             stats.FlushNs.Load() / 1_000_000,
		SyncCount:           stats.SyncCount.Load(),
		SyncMs:              stats.SyncNs.Load() / 1_000_000,
		EnqueueWaits:        stats.EnqueueWaits.Load(),
		EnqueueWaitMs:       stats.EnqueueWaitNs.Load() / 1_000_000,
		TimelineGaps:        stats.TimelineGaps.Load(),
		TimelineGapMs:       stats.TimelineGapNs.Load() / 1_000_000,
	}, true
}

func (r *Service) IsRecording(path string) bool {
	base := filepath.Base(path)
	if r.writingFiles.Contains(base) {
		return true
	}
	if !isRecordingSidecarExt(filepath.Ext(base)) {
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

func isRecordingSidecarExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jsonl", ".xml", ".vtt":
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
