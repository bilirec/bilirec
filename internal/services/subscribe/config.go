package subscribe

import (
	"fmt"
	"strconv"

	"github.com/bilirec/bilirec/pkg/pool"
	"go.etcd.io/bbolt"
)

type RoomConfig struct {
	AutoRecord            bool
	Notify                bool
	RecordDurationMinutes int // 0 = system default, -1 = unlimited, >0 = custom minutes
	Qn                    int  // 0 = backend default (原画); see bilibili.Quality
	OnlyAudio             bool // request audio-only stream when starting recording
}

var roomConfigSerializer = pool.NewSerializer()
var defaultRoomConfigBytes = mustSerializeRoomConfig(defaultRoomConfig())

func mustSerializeRoomConfig(cfg *RoomConfig) []byte {
	data, err := roomConfigSerializer.Serialize(cfg)
	if err != nil {
		panic(err)
	}
	return data
}

func defaultRoomConfig() *RoomConfig {
	return &RoomConfig{AutoRecord: false, Notify: false, RecordDurationMinutes: 0}
}

func (s *Service) UpdateConfig(roomID int, cfg *RoomConfig) error {
	var data []byte
	if cfg == nil {
		data = defaultRoomConfigBytes
	} else {
		encoded, err := roomConfigSerializer.Serialize(cfg)
		if err != nil {
			return err
		}
		data = encoded
	}

	key := fmt.Append(nil, roomID)
	return s.bucket.Update(func(bucket *bbolt.Bucket) error {
		exists := bucket.Get(key)
		if exists == nil {
			return ErrRoomNotSubscribed
		}
		return bucket.Put(key, data)
	})
}

func (s *Service) ListSubscribedRoomsWithConfig() (map[int]*RoomConfig, error) {
	result := make(map[int]*RoomConfig)
	err := s.bucket.ForEach(func(k, v []byte) error {
		roomID, err := strconv.Atoi(string(k))
		if err != nil {
			logger.Warnf("扫描条目失败：%s：%v，已忽略。", string(k), err)
			return nil
		}

		cfg, err := parseRoomConfig(v)
		if err != nil {
			logger.Warnf("解析房间 %d 配置失败：%v，使用默认值", roomID, err)
			cfg = defaultRoomConfig()
		}
		result[roomID] = cfg
		return nil
	})
	return result, err
}

func parseRoomConfig(raw []byte) (*RoomConfig, error) {
	if len(raw) == 0 {
		return defaultRoomConfig(), nil
	}

	cfg := &RoomConfig{}
	if err := roomConfigSerializer.Deserialize(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *Service) GetConfig(roomID int) (*RoomConfig, error) {
	key := fmt.Append(nil, roomID)

	var cfg *RoomConfig
	err := s.bucket.View(func(bucket *bbolt.Bucket) error {
		raw := bucket.Get(key)
		if raw == nil {
			return ErrRoomNotSubscribed
		}

		parsed, err := parseRoomConfig(raw)
		if err != nil {
			return err
		}
		cfg = parsed
		return nil
	})
	return cfg, err
}
