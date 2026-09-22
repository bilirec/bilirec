package webhook

import (
	"os"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

func (s *Service) SessionStarted(room *bilibili.LiveRoomInfoDetail, sessionID string, recording, danmakuConnected bool) {
	if !s.enabled() || room == nil || sessionID == "" {
		return
	}
	data := baseRoomData(room, sessionID, recording, danmakuConnected)
	s.emit(EventSessionStarted, data)
}

func (s *Service) SessionEnded(room *bilibili.LiveRoomInfoDetail, sessionID string, recording, danmakuConnected bool) {
	if !s.enabled() || room == nil || sessionID == "" {
		return
	}
	data := baseRoomData(room, sessionID, recording, danmakuConnected)
	s.emit(EventSessionEnded, data)
}

func (s *Service) StreamStarted(room *bilibili.LiveRoomInfoDetail) {
	if !s.enabled() || room == nil {
		return
	}
	data := baseRoomData(room, "", false, false)
	s.emit(EventStreamStarted, data)
}

func (s *Service) StreamEnded(room *bilibili.LiveRoomInfoDetail) {
	if !s.enabled() || room == nil {
		return
	}
	data := baseRoomData(room, "", false, false)
	s.emit(EventStreamEnded, data)
}

func (s *Service) FileOpening(room *bilibili.LiveRoomInfoDetail, sessionID string, absPath string, fileOpen time.Time, recording, danmakuConnected bool) {
	if !s.enabled() || room == nil || absPath == "" {
		return
	}
	rel, err := s.relativePath(absPath)
	if err != nil {
		log.Warnf("webhook FileOpening 相对路径失败：%v", err)
		return
	}
	data := baseRoomData(room, sessionID, recording, danmakuConnected)
	data.RelativePath = rel
	data.FileOpenTime = fileOpen.Format(time.RFC3339Nano)
	s.emit(EventFileOpening, data)
}

func (s *Service) FileClosed(room *bilibili.LiveRoomInfoDetail, sessionID string, absPath string, fileOpen, fileClose time.Time, recording, danmakuConnected bool) {
	if !s.enabled() || room == nil || absPath == "" {
		return
	}
	rel, err := s.relativePath(absPath)
	if err != nil {
		log.Warnf("webhook FileClosed 相对路径失败：%v", err)
		return
	}
	info, err := os.Stat(absPath)
	if err != nil {
		log.Warnf("webhook FileClosed 无法读取文件：%s %v", absPath, err)
		return
	}
	duration := fileClose.Sub(fileOpen).Seconds()
	if duration < 0 {
		duration = 0
	}
	data := baseRoomData(room, sessionID, recording, danmakuConnected)
	data.RelativePath = rel
	data.FileOpenTime = fileOpen.Format(time.RFC3339Nano)
	data.FileCloseTime = fileClose.Format(time.RFC3339Nano)
	data.FileSize = info.Size()
	data.Duration = duration
	s.emit(EventFileClosed, data)
}
