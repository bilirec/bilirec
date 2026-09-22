package subcheck

import "github.com/bilirec/bilirec/internal/modules/bilibili"

func (s *Service) emitStreamStarted(room *bilibili.LiveRoomInfoDetail) {
	if !s.webhookOn || s.wh == nil || room == nil {
		return
	}
	s.wh.StreamStarted(room)
}

func (s *Service) emitStreamEnded(room *bilibili.LiveRoomInfoDetail) {
	if !s.webhookOn || s.wh == nil || room == nil {
		return
	}
	s.wh.StreamEnded(room)
}
