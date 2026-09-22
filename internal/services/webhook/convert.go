package webhook

import (
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/utils"
)

// OnConvertTaskSuccess is invoked when a transcode task completes successfully.
func (s *Service) OnConvertTaskSuccess(inputPath, outputPath string) {
	if !s.enabled() {
		return
	}
	meta := s.PopConvertSegmentMeta(inputPath)
	if meta != nil {
		s.EmitConvertSegment(meta, outputPath)
	}
}

// EmitConvertSegment fires FileOpening and FileClosed for a finished transcode output.
func (s *Service) EmitConvertSegment(meta *SegmentMeta, absPath string) {
	if !s.enabled() || meta == nil || absPath == "" {
		return
	}
	if !utils.IsFileExists(absPath) {
		log.Warnf("webhook 转码完成但输出不存在：%s", absPath)
		return
	}
	fileOpen, err := time.Parse(time.RFC3339Nano, meta.FileOpenTime)
	if err != nil {
		fileOpen = time.Now()
	}
	fileClose := time.Now()
	room := &bilibili.LiveRoomInfoDetail{
		RoomID:         int64(meta.RoomID),
		ShortID:        meta.ShortID,
		Uname:          meta.Name,
		Title:          meta.Title,
		ParentAreaName: meta.AreaNameParent,
		AreaName:       meta.AreaNameChild,
		LiveStatus:     utils.Ternary(meta.Streaming, 1, 0),
	}
	s.FileOpening(room, meta.SessionID, absPath, fileOpen, meta.Recording, meta.DanmakuConnected)
	s.FileClosed(room, meta.SessionID, absPath, fileOpen, fileClose, meta.Recording, meta.DanmakuConnected)
}

// RegisterConvertSegmentMeta associates recorder segment metadata with a convert
// task input path. PopConvertSegmentMeta removes it when the transcode succeeds.
func (s *Service) RegisterConvertSegmentMeta(inputPath string, meta *SegmentMeta) {
	if !s.enabled() || meta == nil || inputPath == "" {
		return
	}
	key, err := convertInputKey(inputPath)
	if err != nil {
		log.Warnf("webhook 注册转码元数据失败：%v", err)
		return
	}
	s.convertMetaMu.Lock()
	if s.convertMeta == nil {
		s.convertMeta = make(map[string]*SegmentMeta)
	}
	s.convertMeta[key] = meta
	s.convertMetaMu.Unlock()
}

// PopConvertSegmentMeta returns metadata registered for inputPath, if any.
func (s *Service) PopConvertSegmentMeta(inputPath string) *SegmentMeta {
	if !s.enabled() || inputPath == "" {
		return nil
	}
	key, err := convertInputKey(inputPath)
	if err != nil {
		return nil
	}
	s.convertMetaMu.Lock()
	meta := s.convertMeta[key]
	delete(s.convertMeta, key)
	s.convertMetaMu.Unlock()
	return meta
}
