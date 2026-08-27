package subscribe

import (
	"slices"
	"strconv"
)

func cloneRoomConfig(cfg *RoomConfig) *RoomConfig {
	if cfg == nil {
		return defaultRoomConfig()
	}
	out := *cfg
	if cfg.StreamProfiles != nil {
		out.StreamProfiles = slices.Clone(cfg.StreamProfiles)
	}
	return &out
}

func cloneRooms(rooms map[int]*RoomConfig) map[int]*RoomConfig {
	out := make(map[int]*RoomConfig, len(rooms))
	for id, cfg := range rooms {
		out[id] = cloneRoomConfig(cfg)
	}
	return out
}

func (s *Service) loadRooms() error {
	rooms := make(map[int]*RoomConfig)
	err := s.bucket.ForEach(func(k, v []byte) error {
		roomID, err := strconv.Atoi(string(k))
		if err != nil {
			log.Warnf("扫描条目失败：%s：%v，已忽略。", string(k), err)
			return nil
		}

		cfg, err := parseRoomConfig(v)
		if err != nil {
			log.Warnf("解析房间 %d 配置失败：%v，使用默认值", roomID, err)
			cfg = defaultRoomConfig()
		}
		rooms[roomID] = cfg
		return nil
	})
	if err != nil {
		return err
	}

	s.roomsMu.Lock()
	s.rooms = rooms
	s.roomsMu.Unlock()
	return nil
}
