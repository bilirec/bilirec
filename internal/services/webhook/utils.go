package webhook

import (
	"path/filepath"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

// convertInputKey normalizes a filesystem path for the in-memory convert-meta map.
func convertInputKey(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func streamingFromRoom(room *bilibili.LiveRoomInfoDetail) bool {
	if room == nil {
		return false
	}
	return room.LiveStatus == 1
}

func baseRoomData(room *bilibili.LiveRoomInfoDetail, sessionID string, recording, danmakuConnected bool) roomEventData {
	streaming := streamingFromRoom(room)
	shortID := int64(0)
	name, title, parent, child := "", "", "", ""
	roomID := 0
	if room != nil {
		roomID = int(room.RoomID)
		shortID = room.ShortID
		name = room.Uname
		title = room.Title
		parent = room.ParentAreaName
		child = room.AreaName
	}
	return roomEventData{
		SessionID:        sessionID,
		RoomID:           roomID,
		ShortID:          shortID,
		Name:             name,
		Title:            title,
		AreaNameParent:   parent,
		AreaNameChild:    child,
		Recording:        recording,
		Streaming:        streaming,
		DanmakuConnected: danmakuConnected,
	}
}

// relativePath maps an absolute file path to OUTPUT_DIR-relative slash path for BililiveRecorder payloads.
func (s *Service) relativePath(absPath string) (string, error) {
	outDir, err := filepath.Abs(s.outputDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(outDir, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
